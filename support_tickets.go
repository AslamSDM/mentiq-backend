package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type SupportTicket struct {
	ID          string     `gorm:"primaryKey" json:"id"`
	AccountID   string     `gorm:"index;not null" json:"account_id"`
	UserID      *string    `gorm:"index" json:"user_id"` // Nullable - may not have user record for JWT auth
	Subject     string     `gorm:"not null" json:"subject"`
	Description string     `gorm:"type:text;not null" json:"description"`
	Priority    string     `gorm:"default:'medium'" json:"priority"`
	Status      string     `gorm:"default:'open'" json:"status"`
	Category    string     `gorm:"default:'general'" json:"category"`
	AssignedTo  *string    `json:"assigned_to"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Account  Account         `gorm:"foreignKey:AccountID;references:ID" json:"account,omitempty"`
	User     *User           `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"` // Pointer since UserID is nullable
	Assignee *User           `gorm:"foreignKey:AssignedTo;references:ID" json:"assignee,omitempty"`
	Comments []TicketComment `gorm:"foreignKey:TicketID" json:"comments,omitempty"`
}

func (SupportTicket) TableName() string {
	return "support_tickets"
}

type TicketComment struct {
	ID         string    `gorm:"primaryKey" json:"id"`
	TicketID   string    `gorm:"index;not null" json:"ticket_id"`
	UserID     *string   `gorm:"" json:"user_id"` // Nullable - may not have user record
	Content    string    `gorm:"type:text;not null" json:"content"`
	IsInternal bool      `gorm:"default:false" json:"is_internal"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	Ticket SupportTicket `gorm:"foreignKey:TicketID;references:ID" json:"ticket,omitempty"`
	User   *User         `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"` // Pointer since UserID is nullable
}

func (TicketComment) TableName() string {
	return "ticket_comments"
}

type CreateTicketRequest struct {
	Subject     string `json:"subject" binding:"required,min=3,max=200"`
	Description string `json:"description" binding:"required,min=10,max=5000"`
	Priority    string `json:"priority"`
	Category    string `json:"category"`
}

type UpdateTicketRequest struct {
	Subject     *string `json:"subject"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
	Status      *string `json:"status"`
	Category    *string `json:"category"`
	AssignedTo  *string `json:"assigned_to"`
}

type CreateCommentRequest struct {
	Content    string `json:"content" binding:"required,min=1,max=5000"`
	IsInternal bool   `json:"is_internal"`
}

var validPriorities = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true}
var validStatuses = map[string]bool{"open": true, "in_progress": true, "resolved": true, "closed": true}
var validCategories = map[string]bool{"bug": true, "feature_request": true, "billing": true, "general": true, "technical": true}

// Helper function to get account ID and user ID from context
// Returns accountID, userID (nullable), isAdmin, error
func getAuthContext(c *gin.Context, db *gorm.DB) (string, *string, bool, error) {
	userID := c.GetString("user_id")
	accountID, accountExists := c.Get("account_id")
	isAdmin := c.GetBool("is_admin")

	var accountIDStr string
	var userIDPtr *string

	if userID != "" {
		// We have a user_id, get account from user
		var user User
		if err := db.First(&user, "id = ?", userID).Error; err == nil {
			accountIDStr = user.AccountID
			userIDPtr = &userID
			if user.Role == "admin" {
				isAdmin = true
			}
		}
	}

	// Fallback to account_id from JWT context
	if accountIDStr == "" && accountExists && accountID != nil {
		accountIDStr = accountID.(string)
	}

	if accountIDStr == "" {
		return "", nil, false, gorm.ErrRecordNotFound
	}

	return accountIDStr, userIDPtr, isAdmin, nil
}

func CreateTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr, userIDPtr, _, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		var req CreateTicketRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		priority := "medium"
		if req.Priority != "" {
			if !validPriorities[req.Priority] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid priority"})
				return
			}
			priority = req.Priority
		}

		category := "general"
		if req.Category != "" {
			if !validCategories[req.Category] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category"})
				return
			}
			category = req.Category
		}

		ticket := SupportTicket{
			ID:          uuid.New().String(),
			AccountID:   accountIDStr,
			UserID:      userIDPtr, // Nil if user lookup failed
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

		db.Preload("User").Preload("Account").First(&ticket, "id = ?", ticket.ID)
		c.JSON(http.StatusCreated, ticket)
	}
}

func GetTicketsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr, _, _, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		var tickets []SupportTicket
		query := db.Where("account_id = ?", accountIDStr)

		if status := c.Query("status"); status != "" && validStatuses[status] {
			query = query.Where("status = ?", status)
		}
		if priority := c.Query("priority"); priority != "" && validPriorities[priority] {
			query = query.Where("priority = ?", priority)
		}
		if category := c.Query("category"); category != "" && validCategories[category] {
			query = query.Where("category = ?", category)
		}

		query.Preload("User").Order("created_at DESC").Find(&tickets)
		c.JSON(http.StatusOK, tickets)
	}
}

func GetTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr, _, isAdmin, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")
		if ticketID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Ticket ID required"})
			return
		}

		var ticket SupportTicket
		if err := db.Preload("User").Preload("Account").Preload("Assignee").Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC").Preload("User")
		}).First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Check access - must own ticket or be admin
		if ticket.AccountID != accountIDStr && !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		// Filter internal comments for non-admins
		if !isAdmin {
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

func UpdateTicketHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr, _, isAdmin, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")
		if ticketID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Ticket ID required"})
			return
		}

		var ticket SupportTicket
		if err := db.First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Check access - must own ticket or be admin
		if ticket.AccountID != accountIDStr && !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		var req UpdateTicketRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if req.Subject != nil {
			ticket.Subject = *req.Subject
		}
		if req.Description != nil {
			ticket.Description = *req.Description
		}
		if req.Priority != nil {
			if !validPriorities[*req.Priority] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid priority"})
				return
			}
			ticket.Priority = *req.Priority
		}
		if req.Status != nil {
			if !validStatuses[*req.Status] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
				return
			}
			ticket.Status = *req.Status
			if *req.Status == "resolved" || *req.Status == "closed" {
				now := time.Now()
				ticket.ResolvedAt = &now
			}
		}
		if req.Category != nil {
			if !validCategories[*req.Category] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category"})
				return
			}
			ticket.Category = *req.Category
		}
		if req.AssignedTo != nil && isAdmin {
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

func AddCommentHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr, userIDPtr, isAdmin, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		ticketID := c.Param("id")
		if ticketID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Ticket ID required"})
			return
		}

		var ticket SupportTicket
		if err := db.First(&ticket, "id = ?", ticketID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Ticket not found"})
			return
		}

		// Check access - must own ticket or be admin
		if ticket.AccountID != accountIDStr && !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		var req CreateCommentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		isInternalComment := req.IsInternal && isAdmin

		comment := TicketComment{
			ID:         uuid.New().String(),
			TicketID:   ticketID,
			UserID:     userIDPtr, // May be nil
			Content:    req.Content,
			IsInternal: isInternalComment,
		}

		if err := db.Create(&comment).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add comment"})
			return
		}

		// Reopen ticket if user adds comment to resolved/closed ticket
		if !isAdmin && (ticket.Status == "resolved" || ticket.Status == "closed") {
			ticket.Status = "open"
			ticket.ResolvedAt = nil
			db.Save(&ticket)
		}

		db.Preload("User").First(&comment, "id = ?", comment.ID)
		c.JSON(http.StatusCreated, comment)
	}
}

func GetAllTicketsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, _, isAdmin, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		if !isAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		var tickets []SupportTicket
		query := db.Model(&SupportTicket{})

		if status := c.Query("status"); status != "" && validStatuses[status] {
			query = query.Where("status = ?", status)
		}
		if priority := c.Query("priority"); priority != "" && validPriorities[priority] {
			query = query.Where("priority = ?", priority)
		}
		if category := c.Query("category"); category != "" && validCategories[category] {
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

func GetTicketStatsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		_, _, isAdmin, err := getAuthContext(c, db)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		if !isAdmin {
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
