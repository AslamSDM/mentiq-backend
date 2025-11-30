package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreateOrUpdateSubscriptionRequest represents the request body for creating/updating a subscription
type CreateOrUpdateSubscriptionRequest struct {
	AccountID            string  `json:"account_id" binding:"required"`
	Tier                 *string `json:"tier"`
	UserCount            *int    `json:"user_count"`
	MonthlyPrice         *int64  `json:"monthly_price"`
	Status               *string `json:"status"`
	StripeSubscriptionID *string `json:"stripe_subscription_id"`
	StripeCustomerID     *string `json:"stripe_customer_id"`
	StripePriceID        *string `json:"stripe_price_id"`
	StripeProductID      *string `json:"stripe_product_id"`
	CurrentPeriodStart   *string `json:"current_period_start"`
	CurrentPeriodEnd     *string `json:"current_period_end"`
	TrialStart           *string `json:"trial_start"`
	TrialEnd             *string `json:"trial_end"`
	CancelAtPeriodEnd    *bool   `json:"cancel_at_period_end"`
	CanceledAt           *string `json:"canceled_at"`
	CancellationReason   *string `json:"cancellation_reason"`
}

// CreatePaymentRequest represents the request body for recording a payment
type CreatePaymentRequest struct {
	AccountID       string  `json:"account_id" binding:"required"`
	Amount          int64   `json:"amount" binding:"required"`
	Currency        string  `json:"currency"`
	Status          string  `json:"status" binding:"required"`
	StripeInvoiceID *string `json:"stripe_invoice_id"`
	StripeChargeID  *string `json:"stripe_charge_id"`
	StripePaymentID *string `json:"stripe_payment_id"`
	Description     *string `json:"description"`
	InvoiceNumber   *string `json:"invoice_number"`
	InvoicePDF      *string `json:"invoice_pdf"`
	PaidAt          *string `json:"paid_at"`
	FailedAt        *string `json:"failed_at"`
	RefundedAt      *string `json:"refunded_at"`
	RefundAmount    *int64  `json:"refund_amount"`
	RefundReason    *string `json:"refund_reason"`
}

// createOrUpdateSubscriptionHandler handles creating or updating a subscription
func (s *Server) createOrUpdateSubscriptionHandler(c *gin.Context) {
	var req CreateOrUpdateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify account exists
	var account Account
	if err := s.db.Where("id = ?", req.AccountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Check if subscription already exists for this account
	var existingSubscription AccountSubscription
	err := s.db.Where("account_id = ?", req.AccountID).First(&existingSubscription).Error

	if err == nil {
		// Update existing subscription
		updateData := make(map[string]interface{})

		if req.Tier != nil {
			updateData["tier"] = *req.Tier
		}
		if req.UserCount != nil {
			updateData["user_count"] = *req.UserCount
		}
		if req.MonthlyPrice != nil {
			updateData["monthly_price"] = *req.MonthlyPrice
		}
		if req.Status != nil {
			updateData["status"] = *req.Status
		}
		if req.StripeSubscriptionID != nil {
			updateData["stripe_subscription_id"] = *req.StripeSubscriptionID
		}
		if req.StripePriceID != nil {
			updateData["stripe_price_id"] = *req.StripePriceID
		}
		if req.StripeProductID != nil {
			updateData["stripe_product_id"] = *req.StripeProductID
		}
		if req.CurrentPeriodStart != nil {
			if startTime, err := time.Parse(time.RFC3339, *req.CurrentPeriodStart); err == nil {
				updateData["current_period_start"] = startTime
			}
		}
		if req.CurrentPeriodEnd != nil {
			if endTime, err := time.Parse(time.RFC3339, *req.CurrentPeriodEnd); err == nil {
				updateData["current_period_end"] = endTime
			}
		}
		if req.TrialStart != nil {
			if trialStart, err := time.Parse(time.RFC3339, *req.TrialStart); err == nil {
				updateData["trial_start"] = &trialStart
			}
		}
		if req.TrialEnd != nil {
			if trialEnd, err := time.Parse(time.RFC3339, *req.TrialEnd); err == nil {
				updateData["trial_end"] = &trialEnd
			}
		}
		if req.CancelAtPeriodEnd != nil {
			updateData["cancel_at_period_end"] = *req.CancelAtPeriodEnd
		}
		if req.CanceledAt != nil {
			if canceledAt, err := time.Parse(time.RFC3339, *req.CanceledAt); err == nil {
				updateData["canceled_at"] = &canceledAt
			}
		}
		if req.CancellationReason != nil {
			updateData["cancellation_reason"] = *req.CancellationReason
		}

		updateData["updated_at"] = time.Now()

		if err := s.db.Model(&existingSubscription).Updates(updateData).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update subscription"})
			return
		}

		// Fetch updated subscription
		s.db.Where("id = ?", existingSubscription.ID).First(&existingSubscription)

		c.JSON(http.StatusOK, gin.H{
			"message":      "Subscription updated successfully",
			"subscription": existingSubscription,
		})
		return
	}

	// Create new subscription
	subscription := AccountSubscription{
		ID:        uuid.New().String(),
		AccountID: req.AccountID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if req.Tier != nil {
		subscription.Tier = *req.Tier
	}
	if req.UserCount != nil {
		subscription.UserCount = *req.UserCount
	}
	if req.MonthlyPrice != nil {
		subscription.MonthlyPrice = *req.MonthlyPrice
	}
	if req.Status != nil {
		subscription.Status = *req.Status
	} else {
		subscription.Status = "active"
	}
	if req.StripeSubscriptionID != nil {
		subscription.StripeSubscriptionID = *req.StripeSubscriptionID
	}
	if req.StripePriceID != nil {
		subscription.StripePriceID = *req.StripePriceID
	}
	if req.StripeProductID != nil {
		subscription.StripeProductID = *req.StripeProductID
	}
	if req.CurrentPeriodStart != nil {
		if startTime, err := time.Parse(time.RFC3339, *req.CurrentPeriodStart); err == nil {
			subscription.CurrentPeriodStart = startTime
		}
	}
	if req.CurrentPeriodEnd != nil {
		if endTime, err := time.Parse(time.RFC3339, *req.CurrentPeriodEnd); err == nil {
			subscription.CurrentPeriodEnd = endTime
		}
	}
	if req.TrialStart != nil {
		if trialStart, err := time.Parse(time.RFC3339, *req.TrialStart); err == nil {
			subscription.TrialStart = &trialStart
		}
	}
	if req.TrialEnd != nil {
		if trialEnd, err := time.Parse(time.RFC3339, *req.TrialEnd); err == nil {
			subscription.TrialEnd = &trialEnd
		}
	}

	if err := s.db.Create(&subscription).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create subscription"})
		return
	}

	// Update account with Stripe customer ID if provided
	if req.StripeCustomerID != nil && *req.StripeCustomerID != "" {
		s.db.Model(&account).Update("stripe_customer_id", *req.StripeCustomerID)
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":      "Subscription created successfully",
		"subscription": subscription,
	})
}

// getSubscriptionHandler retrieves the subscription for an account
func (s *Server) getSubscriptionHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var subscription AccountSubscription
	if err := s.db.Where("account_id = ?", accountID).First(&subscription).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Subscription not found"})
		return
	}

	c.JSON(http.StatusOK, subscription)
}

// createPaymentHandler records a payment transaction
func (s *Server) createPaymentHandler(c *gin.Context) {
	var req CreatePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify account exists
	var account Account
	if err := s.db.Where("id = ?", req.AccountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Get subscription for this account
	var subscription AccountSubscription
	if err := s.db.Where("account_id = ?", req.AccountID).First(&subscription).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Subscription not found"})
		return
	}

	// Create payment record
	payment := PaymentHistory{
		ID:             uuid.New().String(),
		SubscriptionID: subscription.ID,
		AccountID:      req.AccountID,
		Amount:         req.Amount,
		Status:         req.Status,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if req.Currency != "" {
		payment.Currency = req.Currency
	} else {
		payment.Currency = "usd"
	}

	if req.StripeInvoiceID != nil {
		payment.StripeInvoiceID = *req.StripeInvoiceID
	}
	if req.StripeChargeID != nil {
		payment.StripeChargeID = *req.StripeChargeID
	}
	if req.StripePaymentID != nil {
		payment.StripePaymentID = *req.StripePaymentID
	}
	if req.Description != nil {
		payment.Description = *req.Description
	}
	if req.InvoiceNumber != nil {
		payment.InvoiceNumber = *req.InvoiceNumber
	}
	if req.InvoicePDF != nil {
		payment.InvoicePDF = *req.InvoicePDF
	}
	if req.PaidAt != nil {
		if paidAt, err := time.Parse(time.RFC3339, *req.PaidAt); err == nil {
			payment.PaidAt = &paidAt
		}
	}
	if req.FailedAt != nil {
		if failedAt, err := time.Parse(time.RFC3339, *req.FailedAt); err == nil {
			payment.FailedAt = &failedAt
		}
	}
	if req.RefundedAt != nil {
		if refundedAt, err := time.Parse(time.RFC3339, *req.RefundedAt); err == nil {
			payment.RefundedAt = &refundedAt
		}
	}
	if req.RefundAmount != nil {
		payment.RefundAmount = *req.RefundAmount
	}
	if req.RefundReason != nil {
		payment.RefundReason = req.RefundReason
	}

	if err := s.db.Create(&payment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create payment record"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Payment recorded successfully",
		"payment": payment,
	})
}

// listPaymentsHandler retrieves payment history for an account
func (s *Server) listPaymentsHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var payments []PaymentHistory
	if err := s.db.Where("account_id = ?", accountID).
		Order("created_at DESC").
		Find(&payments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve payments"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payments": payments,
		"count":    len(payments),
	})
}
