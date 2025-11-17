package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/client"
	"github.com/stripe/stripe-go/v72/customer"
	"gorm.io/gorm"
)

// StripeService handles all Stripe-related operations
type StripeService struct {
	db *gorm.DB
}

// NewStripeService creates a new StripeService instance
func NewStripeService(db *gorm.DB) *StripeService {
	return &StripeService{
		db: db,
	}
}

// UpdateStripeAPIKeyRequest represents the request to update Stripe API key
type UpdateStripeAPIKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

// UpdateStripeAPIKeyHandler updates the Stripe API key for a project
func (s *StripeService) UpdateStripeAPIKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	var req UpdateStripeAPIKeyRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate Stripe API key format (accept both secret and restricted keys)
	if len(req.ApiKey) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key format"})
		return
	}

	prefix := req.ApiKey[:8]
	validPrefixes := []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_"}
	isValid := false
	for _, p := range validPrefixes {
		if prefix == p {
			isValid = true
			break
		}
	}

	if !isValid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key format. Please use a Stripe API key (sk_* or rk_*)"})
		return
	}

	// Test the API key by making a simple API call
	stripe.Key = req.ApiKey
	iter := customer.List(&stripe.CustomerListParams{
		ListParams: stripe.ListParams{Limit: stripe.Int64(1)},
	})
	if iter.Err() != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe API key or insufficient permissions"})
		return
	}

	// Update project with Stripe API key
	if err := s.db.Model(&Project{}).Where("id = ?", projectID).Update("stripe_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Stripe API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Stripe API key updated successfully"})
}

// SyncStripeDataHandler synchronizes Stripe data for a project
func (s *StripeService) SyncStripeDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Get project with Stripe API key
	var project Project
	if err := s.db.Where("id = ?", projectID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	if project.StripeAPIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Stripe API key not configured for this project"})
		return
	}

	// Initialize Stripe client with project's API key
	sc := &client.API{}
	sc.Init(project.StripeAPIKey, nil)

	// Sync customers, subscriptions, invoices, and charges
	results := make(map[string]interface{})

	customerCount, err := s.syncCustomers(sc, projectID)
	if err != nil {
		log.Printf("Error syncing customers: %v", err)
		results["customers_error"] = err.Error()
	} else {
		results["customers_synced"] = customerCount
	}

	subscriptionCount, err := s.syncSubscriptions(sc, projectID)
	if err != nil {
		log.Printf("Error syncing subscriptions: %v", err)
		results["subscriptions_error"] = err.Error()
	} else {
		results["subscriptions_synced"] = subscriptionCount
	}

	invoiceCount, err := s.syncInvoices(sc, projectID)
	if err != nil {
		log.Printf("Error syncing invoices: %v", err)
		results["invoices_error"] = err.Error()
	} else {
		results["invoices_synced"] = invoiceCount
	}

	chargeCount, err := s.syncCharges(sc, projectID)
	if err != nil {
		log.Printf("Error syncing charges: %v", err)
		results["charges_error"] = err.Error()
	} else {
		results["charges_synced"] = chargeCount
	}

	// Calculate and cache revenue metrics
	if err := s.calculateRevenueMetrics(projectID); err != nil {
		log.Printf("Error calculating revenue metrics: %v", err)
		results["metrics_error"] = err.Error()
	} else {
		results["metrics_calculated"] = true
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Stripe data sync completed",
		"results": results,
	})
}

// syncCustomers fetches and stores Stripe customers
func (s *StripeService) syncCustomers(sc *client.API, projectID string) (int, error) {
	params := &stripe.CustomerListParams{}
	params.Limit = stripe.Int64(100)

	count := 0
	i := sc.Customers.List(params)
	for i.Next() {
		stripeCustomer := i.Customer()

		// Convert to our model
		customerModel := StripeCustomer{
			ID:         stripeCustomer.ID,
			ProjectID:  projectID,
			Email:      stripeCustomer.Email,
			Name:       stripeCustomer.Name,
			Delinquent: stripeCustomer.Delinquent,
			Balance:    stripeCustomer.Balance,
			Currency:   string(stripeCustomer.Currency),
			Created:    time.Unix(stripeCustomer.Created, 0),
			Deleted:    stripeCustomer.Deleted,
			LastSyncAt: time.Now(),
		}

		// Upsert customer (create or update)
		if err := s.db.Save(&customerModel).Error; err != nil {
			return count, fmt.Errorf("failed to save customer %s: %v", stripeCustomer.ID, err)
		}
		count++
	}

	if err := i.Err(); err != nil {
		return count, fmt.Errorf("stripe API error: %v", err)
	}

	return count, nil
}

// syncSubscriptions fetches and stores Stripe subscriptions
func (s *StripeService) syncSubscriptions(sc *client.API, projectID string) (int, error) {
	params := &stripe.SubscriptionListParams{}
	params.Limit = stripe.Int64(100)
	// Remove status filter to get all subscriptions

	count := 0
	i := sc.Subscriptions.List(params)
	for i.Next() {
		stripeSub := i.Subscription()

		// Get the first price/product info (subscriptions can have multiple items)
		var priceID, productID string
		var unitAmount int64 = 0
		var currency string
		var interval string
		var intervalCount int64 = 1
		var quantity int64 = 1

		if len(stripeSub.Items.Data) > 0 {
			item := stripeSub.Items.Data[0]
			priceID = item.Price.ID
			productID = item.Price.Product.ID
			unitAmount = item.Price.UnitAmount
			currency = string(item.Price.Currency)
			interval = string(item.Price.Recurring.Interval)
			intervalCount = item.Price.Recurring.IntervalCount
			quantity = item.Quantity
		}

		// Convert to our model
		subModel := StripeSubscription{
			ID:                 stripeSub.ID,
			CustomerID:         stripeSub.Customer.ID,
			ProjectID:          projectID,
			Status:             string(stripeSub.Status),
			CurrentPeriodStart: time.Unix(stripeSub.CurrentPeriodStart, 0),
			CurrentPeriodEnd:   time.Unix(stripeSub.CurrentPeriodEnd, 0),
			StartDate:          time.Unix(stripeSub.StartDate, 0),
			Created:            time.Unix(stripeSub.Created, 0),
			PriceID:            priceID,
			ProductID:          productID,
			UnitAmount:         unitAmount,
			Currency:           currency,
			Quantity:           quantity,
			Interval:           interval,
			IntervalCount:      intervalCount,
			LastSyncAt:         time.Now(),
		}

		// Handle optional dates (Stripe uses 0 for null timestamps)
		if stripeSub.TrialStart != 0 {
			trialStart := time.Unix(stripeSub.TrialStart, 0)
			subModel.TrialStart = &trialStart
		}
		if stripeSub.TrialEnd != 0 {
			trialEnd := time.Unix(stripeSub.TrialEnd, 0)
			subModel.TrialEnd = &trialEnd
		}
		if stripeSub.CanceledAt != 0 {
			canceledAt := time.Unix(stripeSub.CanceledAt, 0)
			subModel.CanceledAt = &canceledAt
		}
		if stripeSub.EndedAt != 0 {
			endedAt := time.Unix(stripeSub.EndedAt, 0)
			subModel.EndedAt = &endedAt
		}

		// Upsert subscription
		if err := s.db.Save(&subModel).Error; err != nil {
			return count, fmt.Errorf("failed to save subscription %s: %v", stripeSub.ID, err)
		}
		count++
	}

	if err := i.Err(); err != nil {
		return count, fmt.Errorf("stripe API error: %v", err)
	}

	return count, nil
}

// syncInvoices fetches and stores Stripe invoices
func (s *StripeService) syncInvoices(sc *client.API, projectID string) (int, error) {
	params := &stripe.InvoiceListParams{}
	params.Limit = stripe.Int64(100)

	count := 0
	i := sc.Invoices.List(params)
	for i.Next() {
		stripeInvoice := i.Invoice()

		// Convert to our model
		invoiceModel := StripeInvoice{
			ID:          stripeInvoice.ID,
			CustomerID:  stripeInvoice.Customer.ID,
			ProjectID:   projectID,
			Status:      string(stripeInvoice.Status),
			AmountPaid:  stripeInvoice.AmountPaid,
			AmountDue:   stripeInvoice.AmountDue,
			Subtotal:    stripeInvoice.Subtotal,
			Total:       stripeInvoice.Total,
			Currency:    string(stripeInvoice.Currency),
			PeriodStart: time.Unix(stripeInvoice.PeriodStart, 0),
			PeriodEnd:   time.Unix(stripeInvoice.PeriodEnd, 0),
			Created:     time.Unix(stripeInvoice.Created, 0),
			LastSyncAt:  time.Now(),
		}

		// Handle optional fields
		if stripeInvoice.Subscription != nil {
			invoiceModel.SubscriptionID = &stripeInvoice.Subscription.ID
		}
		if stripeInvoice.DueDate != 0 {
			dueDate := time.Unix(stripeInvoice.DueDate, 0)
			invoiceModel.DueDate = &dueDate
		}
		if stripeInvoice.StatusTransitions.PaidAt != 0 {
			paidAt := time.Unix(stripeInvoice.StatusTransitions.PaidAt, 0)
			invoiceModel.PaidAt = &paidAt
		}

		// Upsert invoice
		if err := s.db.Save(&invoiceModel).Error; err != nil {
			return count, fmt.Errorf("failed to save invoice %s: %v", stripeInvoice.ID, err)
		}
		count++
	}

	if err := i.Err(); err != nil {
		return count, fmt.Errorf("stripe API error: %v", err)
	}

	return count, nil
}

// syncCharges fetches and stores Stripe charges
func (s *StripeService) syncCharges(sc *client.API, projectID string) (int, error) {
	params := &stripe.ChargeListParams{}
	params.Limit = stripe.Int64(100)

	count := 0
	i := sc.Charges.List(params)
	for i.Next() {
		stripeCharge := i.Charge()

		// Convert to our model
		chargeModel := StripeCharge{
			ID:             stripeCharge.ID,
			ProjectID:      projectID,
			Amount:         stripeCharge.Amount,
			AmountCaptured: stripeCharge.AmountCaptured,
			AmountRefunded: stripeCharge.AmountRefunded,
			Currency:       string(stripeCharge.Currency),
			Status:         string(stripeCharge.Status),
			Paid:           stripeCharge.Paid,
			Refunded:       stripeCharge.Refunded,
			Created:        time.Unix(stripeCharge.Created, 0),
			LastSyncAt:     time.Now(),
		}

		// Handle optional fields
		if stripeCharge.Customer != nil {
			chargeModel.CustomerID = &stripeCharge.Customer.ID
		}
		if stripeCharge.Invoice != nil {
			chargeModel.InvoiceID = &stripeCharge.Invoice.ID
		}

		// Upsert charge
		if err := s.db.Save(&chargeModel).Error; err != nil {
			return count, fmt.Errorf("failed to save charge %s: %v", stripeCharge.ID, err)
		}
		count++
	}

	if err := i.Err(); err != nil {
		return count, fmt.Errorf("stripe API error: %v", err)
	}

	return count, nil
}

// calculateRevenueMetrics calculates and stores revenue metrics for a project
func (s *StripeService) calculateRevenueMetrics(projectID string) error {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Calculate MRR from active subscriptions
	var activeSubscriptions []StripeSubscription
	if err := s.db.Where("project_id = ? AND status = ?", projectID, "active").Find(&activeSubscriptions).Error; err != nil {
		return fmt.Errorf("failed to fetch active subscriptions: %v", err)
	}

	var mrr int64 = 0
	for _, sub := range activeSubscriptions {
		monthlyAmount := sub.UnitAmount * sub.Quantity

		// Convert to monthly amount based on interval
		switch sub.Interval {
		case "year":
			monthlyAmount = monthlyAmount / 12
		case "week":
			monthlyAmount = monthlyAmount * 4
		case "day":
			monthlyAmount = monthlyAmount * 30
			// "month" stays as is
		}

		mrr += monthlyAmount
	}

	// Calculate other metrics
	arr := mrr * 12

	// Count subscriptions by status
	var subscriptionCounts struct {
		Active   int64
		Canceled int64
		NewToday int64
		Churned  int64
	}

	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND status = ?", projectID, "active").Count(&subscriptionCounts.Active)
	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND status = ?", projectID, "canceled").Count(&subscriptionCounts.Canceled)
	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND DATE(created) = ?", projectID, today.Format("2006-01-02")).Count(&subscriptionCounts.NewToday)
	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND DATE(canceled_at) = ?", projectID, today.Format("2006-01-02")).Count(&subscriptionCounts.Churned)

	// Calculate total revenue from paid invoices
	var totalRevenue int64
	s.db.Model(&StripeInvoice{}).Where("project_id = ? AND status = ?", projectID, "paid").Select("COALESCE(SUM(amount_paid), 0)").Scan(&totalRevenue)

	// Calculate churn rate
	var churnRate float64
	if subscriptionCounts.Active > 0 {
		churnRate = float64(subscriptionCounts.Canceled) / float64(subscriptionCounts.Active+subscriptionCounts.Canceled) * 100
	}

	// Calculate ARPU (Average Revenue Per User)
	var arpu int64
	if subscriptionCounts.Active > 0 {
		arpu = mrr / subscriptionCounts.Active
	}

	// Calculate trial to paid conversion rate
	var trialToPaidRate float64
	var totalTrials, convertedTrials int64
	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND trial_start IS NOT NULL", projectID).Count(&totalTrials)
	s.db.Model(&StripeSubscription{}).Where("project_id = ? AND trial_start IS NOT NULL AND status = ?", projectID, "active").Count(&convertedTrials)

	if totalTrials > 0 {
		trialToPaidRate = float64(convertedTrials) / float64(totalTrials) * 100
	}

	// Create or update revenue metrics
	metrics := RevenueMetrics{
		ProjectID:                projectID,
		Date:                     today,
		MRR:                      mrr,
		ARR:                      arr,
		TotalRevenue:             totalRevenue,
		ActiveSubscriptions:      int(subscriptionCounts.Active),
		CanceledSubscriptions:    int(subscriptionCounts.Canceled),
		NewSubscriptions:         int(subscriptionCounts.NewToday),
		ChurnedSubscriptions:     int(subscriptionCounts.Churned),
		ChurnRate:                churnRate,
		ARPU:                     arpu,
		TrialToPayConversionRate: trialToPaidRate,
	}

	// Upsert metrics (update if exists for today, create if not)
	if err := s.db.Where("project_id = ? AND date = ?", projectID, today).Save(&metrics).Error; err != nil {
		return fmt.Errorf("failed to save revenue metrics: %v", err)
	}

	return nil
}

// GetRevenueMetricsHandler returns revenue metrics for a project
func (s *StripeService) GetRevenueMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Get date parameter or use today
	dateParam := c.DefaultQuery("date", time.Now().Format("2006-01-02"))
	date, err := time.Parse("2006-01-02", dateParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format. Use YYYY-MM-DD"})
		return
	}

	// Get metrics for the specified date
	var metrics RevenueMetrics
	if err := s.db.Where("project_id = ? AND date = ?", projectID, date).First(&metrics).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "No revenue metrics found for this date. Try syncing Stripe data first."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch revenue metrics"})
		return
	}

	// Convert cents to dollars for display
	response := map[string]interface{}{
		"date":                         metrics.Date.Format("2006-01-02"),
		"mrr":                          float64(metrics.MRR) / 100,
		"arr":                          float64(metrics.ARR) / 100,
		"total_revenue":                float64(metrics.TotalRevenue) / 100,
		"active_subscriptions":         metrics.ActiveSubscriptions,
		"canceled_subscriptions":       metrics.CanceledSubscriptions,
		"new_subscriptions":            metrics.NewSubscriptions,
		"churned_subscriptions":        metrics.ChurnedSubscriptions,
		"expansion_revenue":            float64(metrics.ExpansionRevenue) / 100,
		"contraction_revenue":          float64(metrics.ContractionRevenue) / 100,
		"net_revenue":                  float64(metrics.NetRevenue) / 100,
		"churn_rate":                   metrics.ChurnRate,
		"growth_rate":                  metrics.GrowthRate,
		"arpu":                         float64(metrics.ARPU) / 100,
		"customer_lifetime_value":      float64(metrics.CustomerLifetimeValue) / 100,
		"trial_to_pay_conversion_rate": metrics.TrialToPayConversionRate,
		"last_updated":                 metrics.UpdatedAt,
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
	})
}

// GetRevenueAnalyticsHandler returns comprehensive revenue analytics
func (s *StripeService) GetRevenueAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Get date range parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid start_date format. Use YYYY-MM-DD"})
		return
	}

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid end_date format. Use YYYY-MM-DD"})
		return
	}

	// Get metrics for date range
	var metrics []RevenueMetrics
	if err := s.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("date ASC").Find(&metrics).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch revenue analytics"})
		return
	}

	// Format response
	var timeSeries []map[string]interface{}
	for _, metric := range metrics {
		timeSeries = append(timeSeries, map[string]interface{}{
			"date":                 metric.Date.Format("2006-01-02"),
			"mrr":                  float64(metric.MRR) / 100,
			"arr":                  float64(metric.ARR) / 100,
			"active_subscriptions": metric.ActiveSubscriptions,
			"churn_rate":           metric.ChurnRate,
			"arpu":                 float64(metric.ARPU) / 100,
		})
	}

	// Get latest metrics for summary
	var latestMetrics RevenueMetrics
	if len(metrics) > 0 {
		latestMetrics = metrics[len(metrics)-1]
	}

	response := map[string]interface{}{
		"summary": map[string]interface{}{
			"current_mrr":           float64(latestMetrics.MRR) / 100,
			"current_arr":           float64(latestMetrics.ARR) / 100,
			"active_subscriptions":  latestMetrics.ActiveSubscriptions,
			"churn_rate":            latestMetrics.ChurnRate,
			"arpu":                  float64(latestMetrics.ARPU) / 100,
			"trial_conversion_rate": latestMetrics.TrialToPayConversionRate,
		},
		"time_series": timeSeries,
		"date_range": map[string]string{
			"start": startDate,
			"end":   endDate,
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
	})
}

// GetCustomerAnalyticsHandler returns customer-focused analytics
func (s *StripeService) GetCustomerAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Customer segmentation by revenue
	type CustomerSegment struct {
		CustomerID string  `json:"customer_id"`
		Email      string  `json:"email"`
		Name       string  `json:"name"`
		MRR        float64 `json:"mrr"`
		Status     string  `json:"status"`
		Created    string  `json:"created"`
	}

	var segments []CustomerSegment
	query := `
		SELECT 
			c.id as customer_id,
			c.email,
			c.name,
			COALESCE(SUM(s.unit_amount * s.quantity), 0) / 100.0 as mrr,
			CASE WHEN COUNT(s.id) > 0 THEN 'subscribed' ELSE 'free' END as status,
			c.created
		FROM stripe_customers c
		LEFT JOIN stripe_subscriptions s ON c.id = s.customer_id AND s.status = 'active'
		WHERE c.project_id = ?
		GROUP BY c.id, c.email, c.name, c.created
		ORDER BY mrr DESC
	`

	if err := s.db.Raw(query, projectID).Scan(&segments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch customer analytics"})
		return
	}

	// Calculate totals
	totalCustomers := len(segments)
	paidCustomers := 0
	totalMRR := 0.0

	for _, segment := range segments {
		if segment.MRR > 0 {
			paidCustomers++
			totalMRR += segment.MRR
		}
	}

	// Calculate conversion rate (avoid division by zero)
	conversionRate := 0.0
	if totalCustomers > 0 {
		conversionRate = float64(paidCustomers) / float64(totalCustomers) * 100
	}

	response := map[string]interface{}{
		"summary": map[string]interface{}{
			"total_customers": totalCustomers,
			"paid_customers":  paidCustomers,
			"free_customers":  totalCustomers - paidCustomers,
			"total_mrr":       totalMRR,
			"conversion_rate": conversionRate,
		},
		"customer_segments": segments,
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
	})
}
