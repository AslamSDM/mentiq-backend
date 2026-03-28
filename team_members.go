package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// UpdateMemberRoleRequest represents a request to update a team member's role
type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// listAccountMembersHandler lists all team members for an account
func (s *Server) listAccountMembersHandler(c *gin.Context) {
	accountID := c.GetString("account_id")

	// Get all active users in the account
	var users []User
	if err := s.db.Where("account_id = ?", accountID).Order("created_at ASC").Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch team members"})
		return
	}

	// Get account subscription to return tier limits
	var subscription AccountSubscription
	tier := "starter"
	if err := s.db.Where("account_id = ?", accountID).First(&subscription).Error; err == nil {
		tier = subscription.Tier
	}

	limit := getTeamLimitForTier(tier)

	c.JSON(http.StatusOK, gin.H{
		"members": users,
		"total":   len(users),
		"limit":   limit,
		"tier":    tier,
	})
}

// updateAccountMemberHandler updates a team member's role
func (s *Server) updateAccountMemberHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	currentUserID := c.GetString("user_id")
	memberID := c.Param("id")

	var req UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate role
	validRoles := map[string]bool{"owner": true, "admin": true, "member": true, "viewer": true}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be owner, admin, member, or viewer"})
		return
	}

	// Get current user's role to check permissions
	var currentUser User
	if err := s.db.Where("id = ? AND account_id = ?", currentUserID, accountID).First(&currentUser).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	// Only owners and admins can change roles
	if currentUser.Role != "owner" && currentUser.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners and admins can change member roles"})
		return
	}

	// Get the member to update
	var member User
	if err := s.db.Where("id = ? AND account_id = ?", memberID, accountID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team member not found"})
		return
	}

	// Prevent changing the last owner's role
	if member.Role == "owner" && req.Role != "owner" {
		var ownerCount int64
		s.db.Model(&User{}).Where("account_id = ? AND role = ? AND is_active = ?", accountID, "owner", true).Count(&ownerCount)
		if ownerCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot change the last owner's role. Assign another owner first"})
			return
		}
	}

	// Non-owners cannot promote users to owner
	if currentUser.Role != "owner" && req.Role == "owner" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners can promote users to owner role"})
		return
	}

	// Update the member's role
	if err := s.db.Model(&member).Update("role", req.Role).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update member role"})
		return
	}

	// Reload user to get updated data
	s.db.First(&member, "id = ?", memberID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Member role updated successfully",
		"member":  member,
	})
}

// removeAccountMemberHandler removes a team member from the account
func (s *Server) removeAccountMemberHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	currentUserID := c.GetString("user_id")
	memberID := c.Param("id")

	// Get current user's role to check permissions
	var currentUser User
	if err := s.db.Where("id = ? AND account_id = ?", currentUserID, accountID).First(&currentUser).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		return
	}

	// Only owners and admins can remove members
	if currentUser.Role != "owner" && currentUser.Role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only owners and admins can remove team members"})
		return
	}

	// Get the member to remove
	var member User
	if err := s.db.Where("id = ? AND account_id = ?", memberID, accountID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Team member not found"})
		return
	}

	// Prevent removing yourself
	if memberID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot remove yourself. Have another owner or admin remove you"})
		return
	}

	// Prevent removing the last owner
	if member.Role == "owner" {
		var ownerCount int64
		s.db.Model(&User{}).Where("account_id = ? AND role = ? AND is_active = ?", accountID, "owner", true).Count(&ownerCount)
		if ownerCount <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the last owner. Assign another owner first"})
			return
		}
	}

	// Soft delete - set is_active to false instead of deleting
	if err := s.db.Model(&member).Update("is_active", false).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove team member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Team member removed successfully",
	})
}
