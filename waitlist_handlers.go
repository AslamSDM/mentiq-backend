package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// JoinWaitlistRequest represents the request to join the waitlist
type JoinWaitlistRequest struct {
	Email     string `json:"email" binding:"required,email"`
	FullName  string `json:"full_name" binding:"required"`
	Company   string `json:"company"`
	UserCount int    `json:"user_count"`
	Source    string `json:"source"`
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

	// Create new waitlist entry
	waitlistEntry := Waitlist{
		ID:        uuid.New().String(),
		Email:     email,
		FullName:  fullName,
		Company:   req.Company,
		UserCount: req.UserCount,
		Source:    req.Source,
	}

	if err := s.db.Create(&waitlistEntry).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to join waitlist"})
		return
	}

	// Send waitlist confirmation email
	go func() {
		if err := s.emailService.SendWaitlistEmail(email, fullName); err != nil {
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
