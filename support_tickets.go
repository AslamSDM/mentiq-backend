package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SupportTicket represents a customer support ticket
type SupportTicket struct {
	ID          string     `gorm:"primaryKey" json:"id"`
	AccountID   string     `gorm:"index;not null" json:"account_id"`
	UserID      string     `gorm:"index;not null" json:"user_id"`
	Subject     string     `gorm:"not null" json:"subject"`
	Description string     `gorm:"type:text;not null" json:"description"`
	Priority    string     `gorm:"default:'medium'" json:"priority"`  // low, medium, high, urgent
	Status      string     `gorm:"default:'open'" json:"status"`      // open, in_progress, resolved, closed
	Category    string     `gorm:"default:'general'" json:"category"` // bug, feature_request, billing, general, technical
	AssignedTo  *string    `json:"assigned_to"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	// Relations
	Account  Account         `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
	User     User            `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"`
	Assignee *User           `gorm:"foreignKey:AssignedTo;references:ID" json:"assignee,omitempty"`
	Comments []TicketComment `gorm:"foreignKey:TicketID" json:"comments,omitempty"`
}

func (SupportTicket) TableName() string {
	return "support_tickets"
}

// TicketComment represents a comment on a support ticket
type TicketComment struct {
	ID         string    `gorm:"primaryKey" json:"id"`
	TicketID   string    `gorm:"index;not null" json:"ticket_id"`
	UserID     string    `gorm:"not null" json:"user_id"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	IsInternal bool      `gorm:"default:false" json:"is_internal"` // Internal notes visible only to admins
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Relations
	Ticket SupportTicket `gorm:"foreignKey:TicketID;references:ID" json:"ticket,omitempty"`
	User   User          `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"`
}

func (TicketComment) TableName() string {
	return "ticket_comments"
}

// CreateTicketRequest represents the request body for creating a ticket
type CreateTicketRequest struct {
	Subject     string `json:"subject" binding:"required"`
	Description string `json:"description" binding:"required"`
	Priority    string `json:"priority"`
	Category    string `json:"category"`
}

// UpdateTicketRequest represents the request body for updating a ticket
type UpdateTicketRequest struct {
	Subject     *string `json:"subject"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
	Status      *string `json:"status"`
	Category    *string `json:"category"`
	AssignedTo  *string `json:"assigned_to"`
}

// CreateCommentRequest represents the request body for creating a comment
type CreateCommentRequest struct {
	Content    string `json:"content" binding:"required"`
	IsInternal bool   `json:"is_internal"`
}

// CreateTicketHandler creates a new support ticket
func CreateTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Get user and account
		var user User
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
			return
		}

		var req CreateTicketRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Set defaults
		priority := "medium"
		if req.Priority != "" {
			priority = req.Priority
		}
		category := "general"
		if req.Category != "" {
			category = req.Category
		}

		ticket := SupportTicket{
			ID:          uuid.New().String(),
			AccountID:   user.AccountID,
			UserID:      userID,
			Subject:     req.Subject,
			Description: req.Description,
			Priority:    priority,
			Status:      "open",
			Category:    category,
		}

		if err := db.Create(&ticket).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create ticket"})
			return
		}

		// Load relations
		db.Preload("User").Preload("Account").First(&ticket, "id = ?", ticket.ID)

		c.JSON(http.StatusCreated, ticket)
	}
}

// GetTicketsHandler lists tickets for the current user/account
func GetTicketsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Get user
		var user User
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
			return
		}

		var tickets []SupportTicket
		query := db.Where("account_id = ?", user.AccountID)

		// Apply filters
		if status := c.Query("status"); status != "" {
			query = query.Where("status = ?", status)
		}
		if priority := c.Query("priority"); priority != "" {
			query = query.Where("priority = ?", priority)
		}
		if category := c.Query("category"); category != "" {
			query = query.Where("category = ?", category)
		}

		query.Preload("User").Order("created_at DESC").Find(&tickets)

		c.JSON(http.StatusOK, tickets)
	}
}

// GetTicketHandler gets a specific ticket with comments
func GetTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")

		var ticket SupportTicket
		if err := db.Preload("User").Preload("Account").Preload("Assignee").Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC").Preload("User")
		}).First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Get user to check access
		var user User
		db.First(&user, "id = ?", userID)

		// Check if user has access (same account or admin)
		if ticket.AccountID != user.AccountID && user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		// Filter out internal comments for non-admins
		if user.Role != "admin" {
			var publicComments []TicketComment
			for _, comment := range ticket.Comments {
				if !comment.IsInternal {
					publicComments = append(publicComments, comment)
				}
			}
			ticket.Comments = publicComments
		}

		c.JSON(http.StatusOK, ticket)
	}
}

// UpdateTicketHandler updates a ticket
func UpdateTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")

		var ticket SupportTicket
		if err := db.First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Get user to check access
		var user User
		db.First(&user, "id = ?", userID)

		// Check if user has access
		if ticket.AccountID != user.AccountID && user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		var req UpdateTicketRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Update fields
		if req.Subject != nil {
			ticket.Subject = *req.Subject
		}
		if req.Description != nil {
			ticket.Description = *req.Description
		}
		if req.Priority != nil {
			ticket.Priority = *req.Priority
		}
		if req.Status != nil {
			ticket.Status = *req.Status
			if *req.Status == "resolved" || *req.Status == "closed" {
				now := time.Now()
				ticket.ResolvedAt = &now
			}
		}
		if req.Category != nil {
			ticket.Category = *req.Category
		}
		if req.AssignedTo != nil && user.Role == "admin" {
			ticket.AssignedTo = req.AssignedTo
		}

		if err := db.Save(&ticket).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update ticket"})
			return
		}

		db.Preload("User").Preload("Account").Preload("Assignee").First(&ticket, "id = ?", ticket.ID)

		c.JSON(http.StatusOK, ticket)
	}
}

// AddCommentHandler adds a comment to a ticket
func AddCommentHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")

		var ticket SupportTicket
		if err := db.First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Get user to check access
		var user User
		db.First(&user, "id = ?", userID)

		// Check if user has access
		if ticket.AccountID != user.AccountID && user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		var req CreateCommentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// Only admins can create internal comments
		isInternal := req.IsInternal && user.Role == "admin"

		comment := TicketComment{
			ID:         uuid.New().String(),
			TicketID:   ticketID,
			UserID:     userID,
			Content:    req.Content,
			IsInternal: isInternal,
		}

		if err := db.Create(&comment).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add comment"})
			return
		}

		// If ticket was closed/resolved, reopen it when customer comments
		if user.Role != "admin" && (ticket.Status == "resolved" || ticket.Status == "closed") {
			ticket.Status = "open"
			ticket.ResolvedAt = nil
			db.Save(&ticket)
		}

		db.Preload("User").First(&comment, "id = ?", comment.ID)

		c.JSON(http.StatusCreated, comment)
	}
}

// GetAllTicketsHandler gets all tickets (admin only)
func GetAllTicketsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Check if user is admin
		var user User
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
			return
		}

		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		var tickets []SupportTicket
		query := db.Model(&SupportTicket{})

		// Apply filters
		if status := c.Query("status"); status != "" {
			query = query.Where("status = ?", status)
		}
		if priority := c.Query("priority"); priority != "" {
			query = query.Where("priority = ?", priority)
		}
		if category := c.Query("category"); category != "" {
			query = query.Where("category = ?", category)
		}
		if assignedTo := c.Query("assigned_to"); assignedTo != "" {
			if assignedTo == "unassigned" {
				query = query.Where("assigned_to IS NULL")
			} else {
				query = query.Where("assigned_to = ?", assignedTo)
			}
		}

		query.Preload("User").Preload("Account").Preload("Assignee").Order("created_at DESC").Find(&tickets)

		c.JSON(http.StatusOK, tickets)
	}
}

// GetTicketStatsHandler gets ticket statistics (admin only)
func GetTicketStatsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Check if user is admin
		var user User
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
			return
		}

		if user.Role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		var totalCount, openCount, inProgressCount, resolvedCount, closedCount int64
		var urgentCount, highCount int64

		db.Model(&SupportTicket{}).Count(&totalCount)
		db.Model(&SupportTicket{}).Where("status = ?", "open").Count(&openCount)
		db.Model(&SupportTicket{}).Where("status = ?", "in_progress").Count(&inProgressCount)
		db.Model(&SupportTicket{}).Where("status = ?", "resolved").Count(&resolvedCount)
		db.Model(&SupportTicket{}).Where("status = ?", "closed").Count(&closedCount)
		db.Model(&SupportTicket{}).Where("priority = ? AND status NOT IN ?", "urgent", []string{"resolved", "closed"}).Count(&urgentCount)
		db.Model(&SupportTicket{}).Where("priority = ? AND status NOT IN ?", "high", []string{"resolved", "closed"}).Count(&highCount)

		c.JSON(http.StatusOK, gin.H{
			"total":       totalCount,
			"open":        openCount,
			"in_progress": inProgressCount,
			"resolved":    resolvedCount,
			"closed":      closedCount,
			"urgent":      urgentCount,
			"high":        highCount,
		})
	}
}
