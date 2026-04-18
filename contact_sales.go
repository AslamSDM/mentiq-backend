package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ContactRequest struct {
	ID        string     `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"not null" json:"name"`
	Email     string     `gorm:"not null" json:"email"`
	Company   string     `json:"company"`
	Message   string     `json:"message"`
	Source    string     `json:"source"`
	Status    string     `gorm:"default:new" json:"status"` // new, contacted, closed
	Notes     string     `json:"notes"`
	AccountID string     `json:"account_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CreateContactRequest struct {
	Name    string `json:"name" binding:"required"`
	Email   string `json:"email" binding:"required"`
	Company string `json:"company"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

func (s *Server) createContactRequestHandler(c *gin.Context) {
	var req CreateContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	contact := ContactRequest{
		ID:      uuid.New().String(),
		Name:    req.Name,
		Email:   req.Email,
		Company: req.Company,
		Message: req.Message,
		Source:  req.Source,
		Status:  "new",
	}

	accountID, exists := c.Get("account_id")
	if exists {
		contact.AccountID = accountID.(string)
	}

	if err := s.db.Create(&contact).Error; err != nil {
		log.Printf("Failed to create contact request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit request"})
		return
	}

	go s.sendContactSalesEmail(&contact)

	c.JSON(http.StatusOK, gin.H{"message": "Your request has been submitted. Our team will contact you within 24 hours."})
}

func (s *Server) sendContactSalesEmail(contact *ContactRequest) {
	subject := fmt.Sprintf("New Enterprise Inquiry from %s", contact.Name)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="margin:0;padding:0;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;background-color:#FAFAF8;color:#0f172a;">
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color:#FAFAF8;">
<tr><td align="center" style="padding:40px 0;">
<table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="background-color:#ffffff;border-radius:16px;border:1px solid #f1f5f9;box-shadow:0 4px 6px -1px rgba(0,0,0,0.05);">
<tr><td style="padding:40px 40px 30px;text-align:center;">
  <img src="https://backend.trymentiq.com/logo.png" alt="Mentiq" height="32" style="display:block;margin:0 auto;"/>
</td></tr>
<tr><td style="padding:0 40px 40px;">
  <h2 style="margin:0 0 20px;color:#0f172a;font-size:24px;font-weight:600;text-align:center;">New Enterprise Sales Inquiry</h2>
  <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background-color:#f8fafc;border-radius:12px;padding:20px;">
    <tr><td style="padding:20px;">
      <p style="margin:0 0 12px;color:#64748b;font-size:14px;"><strong style="color:#0f172a;">Name:</strong> %s</p>
      <p style="margin:0 0 12px;color:#64748b;font-size:14px;"><strong style="color:#0f172a;">Email:</strong> <a href="mailto:%s" style="color:#3B5BDB;">%s</a></p>
      <p style="margin:0 0 12px;color:#64748b;font-size:14px;"><strong style="color:#0f172a;">Company:</strong> %s</p>
      <p style="margin:0;color:#64748b;font-size:14px;"><strong style="color:#0f172a;">Message:</strong> %s</p>
    </td></tr>
  </table>
  <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0">
    <tr><td align="center" style="padding:30px 0 10px;">
      <a href="mailto:%s?subject=Re: Enterprise Inquiry" style="display:inline-block;background-color:#3B5BDB;color:#ffffff;text-decoration:none;padding:14px 40px;border-radius:12px;font-size:16px;font-weight:500;">Reply to %s</a>
    </td></tr>
  </table>
</td></tr>
<tr><td style="padding:0 40px 30px;text-align:center;">
  <p style="margin:0;color:#94a3b8;font-size:12px;">Source: %s | Submitted: %s</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`,
		contact.Name,
		contact.Email, contact.Email,
		contact.Company,
		contact.Message,
		contact.Email, contact.Name,
		contact.Source,
		contact.CreatedAt.Format("Jan 2, 2006 3:04 PM"),
	)

	if err := s.emailService.sendEmail("info@mentiq.com", "MentiQ Team", subject, html, ""); err != nil {
		log.Printf("Failed to send contact sales email for %s: %v", contact.Email, err)
	}
}

// Admin handlers

func (s *Server) adminListContactRequestsHandler(c *gin.Context) {
	var contacts []ContactRequest
	query := s.db.Order("created_at DESC")

	status := c.Query("status")
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Find(&contacts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch contact requests"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contacts": contacts, "total": len(contacts)})
}

func (s *Server) adminUpdateContactRequestHandler(c *gin.Context) {
	id := c.Param("id")

	var contact ContactRequest
	if err := s.db.Where("id = ?", id).First(&contact).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Contact request not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch contact request"})
		return
	}

	var req struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Notes != "" {
		updates["notes"] = req.Notes
	}

	if err := s.db.Model(&contact).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update contact request"})
		return
	}

	s.db.Where("id = ?", id).First(&contact)
	c.JSON(http.StatusOK, contact)
}
