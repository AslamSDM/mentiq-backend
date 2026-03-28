package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// CreateInvitationRequest represents a request to invite a team member
type CreateInvitationRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role"`
}

// AcceptInvitationRequest represents a request to accept an invitation
type AcceptInvitationRequest struct {
	Token    string `json:"token" binding:"required"`
	FullName string `json:"full_name" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

// createInvitationHandler creates a new team member invitation
func (s *Server) createInvitationHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	inviterEmail := c.GetString("email")

	// Check user role - only owners and admins can invite
	userRole, err := s.getUserRole(accountID, inviterEmail)
	if err != nil || (userRole != "owner" && userRole != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners and admins can invite team members"})
		return
	}

	var req CreateInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Default role to member if not specified
	if req.Role == "" {
		req.Role = "member"
	}

	// Validate role
	validRoles := map[string]bool{"owner": true, "admin": true, "member": true, "viewer": true}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be owner, admin, member, or viewer"})
		return
	}

	// Get account with subscription
	var account Account
	if err := s.db.Preload("AccountSubscription").Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Get or create inviter user record
	var inviterUser User
	if err := s.db.Where("account_id = ? AND email = ?", accountID, inviterEmail).First(&inviterUser).Error; err != nil {
		// If user doesn't exist, create them (for backward compatibility with account-only system)
		inviterUser = User{
			ID:        uuid.New().String(),
			AccountID: accountID,
			Email:     inviterEmail,
			FullName:  account.Name, // Use account name as fallback
			Role:      "owner",      // Assume owner if they're inviting
			IsActive:  true,
		}
		if err := s.db.Create(&inviterUser).Error; err != nil {
			log.Printf("Failed to create inviter user record: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process invitation"})
			return
		}
	}

	// Check team size limits
	var currentUserCount int64
	s.db.Model(&User{}).Where("account_id = ? AND is_active = ?", accountID, true).Count(&currentUserCount)

	var pendingInviteCount int64
	s.db.Model(&UserInvitation{}).Where("account_id = ? AND status = ?", accountID, "pending").Count(&pendingInviteCount)

	totalUsers := int(currentUserCount + pendingInviteCount)
	limit := getTeamLimitForTier(account.AccountSubscription.Tier)

	if limit > 0 && totalUsers >= limit {
		c.JSON(http.StatusForbidden, gin.H{
			"error":            "Team member limit reached for your current plan",
			"limit":            limit,
			"current":          totalUsers,
			"tier":             account.AccountSubscription.Tier,
			"upgrade_required": true,
		})
		return
	}

	// Check if user with this email already exists in the account
	var existingUser User
	if err := s.db.Where("email = ? AND account_id = ?", req.Email, accountID).First(&existingUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User with this email already exists in your team"})
		return
	}

	// Check if invitation already exists
	var existingInvite UserInvitation
	if err := s.db.Where("email = ? AND account_id = ? AND status = ?",
		req.Email, accountID, "pending").First(&existingInvite).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":      "Invitation already sent to this email",
			"invitation": existingInvite,
		})
		return
	}

	// Generate secure token
	token, err := generateSecureToken(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate invitation token"})
		return
	}

	// Create invitation
	invitation := UserInvitation{
		ID:          uuid.New().String(),
		Email:       req.Email,
		Token:       token,
		Role:        req.Role,
		Status:      "pending",
		AccountID:   accountID,
		InvitedByID: inviterUser.ID,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour), // 7 days
	}

	if err := s.db.Create(&invitation).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create invitation"})
		return
	}

	// Load invitation with relations
	s.db.Preload("InvitedBy").Preload("Account").First(&invitation, "id = ?", invitation.ID)

	// Send email asynchronously
	if s.emailService != nil {
		go func() {
			err := s.emailService.SendInvitationEmail(
				req.Email,
				"",
				invitation.InvitedBy.FullName,
				invitation.Account.Name,
				token,
			)
			if err != nil {
				log.Printf("Failed to send invitation email: %v", err)
			}
		}()
	}

	// Mark onboarding task complete
	s.db.Model(&OnboardingStatus{}).
		Where("account_id = ?", accountID).
		Updates(map[string]interface{}{
			"team_members_invited":    true,
			"team_members_invited_at": time.Now(),
		})

	c.JSON(http.StatusCreated, gin.H{
		"message":    "Invitation sent successfully",
		"invitation": invitation,
	})
}

// listInvitationsHandler lists all invitations for an account
func (s *Server) listInvitationsHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	status := c.Query("status") // Optional filter by status

	query := s.db.Where("account_id = ?", accountID)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	var invitations []UserInvitation
	if err := query.Preload("InvitedBy").Order("created_at DESC").Find(&invitations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch invitations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"invitations": invitations,
		"total":       len(invitations),
	})
}

// cancelInvitationHandler cancels a pending invitation
func (s *Server) cancelInvitationHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	email := c.GetString("email")
	invitationID := c.Param("id")

	// Check user role - only owners and admins can cancel invitations
	userRole, err := s.getUserRole(accountID, email)
	if err != nil || (userRole != "owner" && userRole != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners and admins can cancel invitations"})
		return
	}

	var invitation UserInvitation
	if err := s.db.Where("id = ? AND account_id = ?", invitationID, accountID).First(&invitation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invitation not found"})
		return
	}

	if invitation.Status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Can only cancel pending invitations"})
		return
	}

	if err := s.db.Model(&invitation).Update("status", "canceled").Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel invitation"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Invitation canceled",
		"invitation": invitation,
	})
}

// resendInvitationHandler resends an invitation email
func (s *Server) resendInvitationHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	email := c.GetString("email")
	invitationID := c.Param("id")

	// Check user role - only owners and admins can resend invitations
	userRole, err := s.getUserRole(accountID, email)
	if err != nil || (userRole != "owner" && userRole != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners and admins can resend invitations"})
		return
	}

	var invitation UserInvitation
	if err := s.db.Preload("InvitedBy").Preload("Account").Where("id = ? AND account_id = ?", invitationID, accountID).First(&invitation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invitation not found"})
		return
	}

	if invitation.Status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Can only resend pending invitations"})
		return
	}

	// Generate new token and extend expiration
	token, err := generateSecureToken(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate new token"})
		return
	}

	updates := map[string]interface{}{
		"token":      token,
		"expires_at": time.Now().Add(7 * 24 * time.Hour),
	}

	if err := s.db.Model(&invitation).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update invitation"})
		return
	}

	// Resend email asynchronously
	if s.emailService != nil {
		go func() {
			err := s.emailService.SendInvitationEmail(
				invitation.Email,
				"",
				invitation.InvitedBy.FullName,
				invitation.Account.Name,
				token,
			)
			if err != nil {
				log.Printf("Failed to resend invitation email: %v", err)
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Invitation resent",
		"invitation": invitation,
	})
}

// acceptInvitationHandler accepts an invitation and creates a user account
func (s *Server) acceptInvitationHandler(c *gin.Context) {
	var req AcceptInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Find invitation
	var invitation UserInvitation
	if err := s.db.Preload("Account").Where("token = ?", req.Token).First(&invitation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid invitation token"})
		return
	}

	// Check status
	if invitation.Status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invitation is %s and cannot be accepted", invitation.Status)})
		return
	}

	// Check expiration
	if time.Now().After(invitation.ExpiresAt) {
		s.db.Model(&invitation).Update("status", "expired")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invitation has expired"})
		return
	}

	// Check if user already exists with this email
	var existingUser User
	if err := s.db.Where("email = ?", invitation.Email).First(&existingUser).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "A user with this email already exists"})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process password"})
		return
	}

	// Create user
	user := User{
		ID:        uuid.New().String(),
		Email:     invitation.Email,
		Password:  string(hashedPassword),
		FullName:  req.FullName,
		Role:      invitation.Role,
		IsActive:  true,
		AccountID: invitation.AccountID,
	}

	if err := s.db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user account"})
		return
	}

	// Update invitation status
	now := time.Now()
	s.db.Model(&invitation).Updates(map[string]interface{}{
		"status":      "accepted",
		"accepted_at": now,
	})

	// Generate access and refresh tokens
	accessToken, err := GenerateJWT(user.AccountID, user.Email, "access", false, user.Role, 1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := GenerateJWT(user.AccountID, user.Email, "refresh", false, user.Role, 24*7)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	// Store refresh token
	tokenRecord := RefreshToken{
		ID:        uuid.New().String(),
		AccountID: user.AccountID,
		Token:     refreshToken,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		IsRevoked: false,
	}
	s.db.Create(&tokenRecord)

	// Get first project for this account (if any)
	var project Project
	var projectID string
	if err := s.db.Where("account_id = ?", user.AccountID).First(&project).Error; err == nil {
		projectID = project.ID
	}

	// Load account subscription
	var subscription AccountSubscription
	hasActiveSubscription := false
	subscriptionStatus := "none"
	if err := s.db.Where("account_id = ?", user.AccountID).First(&subscription).Error; err == nil {
		hasActiveSubscription = subscription.Status == "active" || subscription.Status == "trialing"
		subscriptionStatus = subscription.Status
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Invitation accepted successfully",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"expires_in":    3600,
		"project_id":    projectID,
		"user": gin.H{
			"id":                      user.ID,
			"name":                    user.FullName,
			"email":                   user.Email,
			"is_admin":                false,
			"has_active_subscription": hasActiveSubscription,
			"subscription_status":     subscriptionStatus,
			"created_at":              user.CreatedAt,
			"updated_at":              user.UpdatedAt,
		},
	})
}

// Helper functions

// generateSecureToken generates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// getTeamLimitForTier returns the team member limit for a given tier
func getTeamLimitForTier(tier string) int {
	limits := map[string]int{
		"starter": 3,
		"growth":  10,
		"scale":   0, // 0 = unlimited
		// Legacy tiers (for existing accounts during migration)
		"launch":     2,
		"traction":   4,
		"momentum":   0,
		"expansion":  0,
		"enterprise": 0,
	}
	if limit, ok := limits[tier]; ok {
		return limit
	}
	return 3 // Default to starter tier limit
}
