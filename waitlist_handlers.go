package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// generateUnsubscribeToken creates a secure random token for unsubscribe links
func generateUnsubscribeToken() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// JoinWaitlistRequest represents the request to join the waitlist
type JoinWaitlistRequest struct {
	Email            string `json:"email" binding:"required,email"`
	FullName         string `json:"full_name" binding:"required"`
	Company          string `json:"company"`
	UserCount        int    `json:"user_count"`
	Source           string `json:"source"`
	PromoEmailsOptIn bool   `json:"promo_emails_opt_in"`
}

// joinWaitlistHandler handles waitlist signups
func (s *Server) joinWaitlistHandler(c *gin.Context) {
	var req JoinWaitlistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email and full name are required"})
		return
	}

	// Normalize email
	email := strings.ToLower(strings.TrimSpace(req.Email))
	fullName := strings.TrimSpace(req.FullName)

	// Check if email already exists
	var existing Waitlist
	if err := s.db.Where("email = ?", email).First(&existing).Error; err == nil {
		// Email already on waitlist
		c.JSON(http.StatusOK, gin.H{
			"message": "You're already on the waitlist! We'll be in touch soon.",
			"success": true,
		})
		return
	}

	// Generate unsubscribe token
	unsubscribeToken := generateUnsubscribeToken()

	// Create new waitlist entry
	waitlistEntry := Waitlist{
		ID:               uuid.New().String(),
		Email:            email,
		FullName:         fullName,
		Company:          req.Company,
		UserCount:        req.UserCount,
		Source:           req.Source,
		PromoEmailsOptIn: req.PromoEmailsOptIn,
		UnsubscribeToken: unsubscribeToken,
	}

	if err := s.db.Create(&waitlistEntry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to join waitlist"})
		return
	}

	// Send waitlist confirmation email
	go func() {
		if err := s.emailService.SendWaitlistEmail(email, fullName, unsubscribeToken); err != nil {
			// Log error but don't fail the request
			println("Failed to send waitlist email:", err.Error())
		} else {
			// Mark email as sent
			s.db.Model(&waitlistEntry).Update("email_sent", true)
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"message": "Welcome to the waitlist! Check your email for confirmation.",
		"success": true,
	})
}

// unsubscribeHandler handles email unsubscribe requests
func (s *Server) unsubscribeHandler(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing unsubscribe token."})
		return
	}

	var entry Waitlist
	if err := s.db.Where("unsubscribe_token = ?", token).First(&entry).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "This unsubscribe link is no longer valid."})
		return
	}

	// Update preferences
	now := time.Now()
	s.db.Model(&entry).Updates(map[string]interface{}{
		"promo_emails_opt_in": false,
		"unsubscribed_at":     now,
	})

	c.JSON(http.StatusOK, gin.H{"message": "You have been unsubscribed successfully. You will no longer receive promotional emails from Mentiq."})
}

// getWaitlistHandler returns all waitlist entries (admin only)
func (s *Server) getWaitlistHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user is admin
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if !account.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}

	var entries []Waitlist
	if err := s.db.Order("created_at DESC").Find(&entries).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch waitlist"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": entries,
		"total":   len(entries),
	})
}

// grantWaitlistAccessHandler grants access to a user on the waitlist (admin only)
func (s *Server) grantWaitlistAccessHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user is admin
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if !account.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}

	waitlistID := c.Param("id")
	if waitlistID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Waitlist ID required"})
		return
	}

	// Find the waitlist entry
	var entry Waitlist
	if err := s.db.Where("id = ?", waitlistID).First(&entry).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Waitlist entry not found"})
		return
	}

	// Check if already granted
	if entry.AccessGranted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Access already granted to this user"})
		return
	}

	// Grant access
	now := time.Now()
	entry.AccessGranted = true
	entry.AccessGrantedAt = &now
	entry.AccessGrantedBy = accountID

	if err := s.db.Save(&entry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to grant access"})
		return
	}

	// Send access granted email
	go func() {
		if err := s.emailService.SendWaitlistAccessGrantedEmail(entry.Email, entry.FullName); err != nil {
			println("Failed to send access granted email:", err.Error())
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"message": "Access granted successfully",
		"entry":   entry,
	})
}

// deleteWaitlistHandler deletes a waitlist entry (admin only)
func (s *Server) deleteWaitlistHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check if user is admin
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	if !account.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}

	waitlistID := c.Param("id")
	if waitlistID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Waitlist ID required"})
		return
	}

	// Find the waitlist entry
	var entry Waitlist
	if err := s.db.Where("id = ?", waitlistID).First(&entry).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Waitlist entry not found"})
		return
	}

	// Delete the entry
	if err := s.db.Delete(&entry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete waitlist entry"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Waitlist entry deleted successfully",
	})
}
