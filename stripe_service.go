package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/client"
	"github.com/stripe/stripe-go/v72/customer"
	"gorm.io/gorm"
)

// StripeService handles all Stripe-related operations with live data fetching
type StripeService struct {
	db    *gorm.DB
	cache *StripeCache
}

// StripeCache provides in-memory caching for Stripe data
type StripeCache struct {
	mu      sync.RWMutex
	entries map[string]*StripeCacheEntry
}

// StripeCacheEntry represents a cached Stripe data entry
type StripeCacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

// Cache TTL constants
const (
	StripeCacheTTL        = 5 * time.Minute  // Live metrics cache
	StripeCustomersTTL    = 10 * time.Minute // Customer list cache
	StripeSubscriptionTTL = 5 * time.Minute  // Subscription cache
)

// NewStripeService creates a new StripeService instance
func NewStripeService(db *gorm.DB) *StripeService {
	return &StripeService{
		db: db,
		cache: &StripeCache{
			entries: make(map[string]*StripeCacheEntry),
		},
	}
}

// getFromCache retrieves data from cache if valid
func (c *StripeCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Data, true
}

// setCache stores data in cache with TTL
func (c *StripeCache) set(key string, data interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &StripeCacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// invalidate removes a specific cache entry
func (c *StripeCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// invalidateProject removes all cache entries for a project
func (c *StripeCache) invalidateProject(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		// Delete keys that start with the project ID
		if len(key) > len(projectID) && key[:len(projectID)] == projectID {
			delete(c.entries, key)
		}
	}
}

// StripeMetrics represents calculated revenue metrics from live Stripe data
type StripeMetrics struct {
	MRR                      float64   `json:"mrr"`
	ARR                      float64   `json:"arr"`
	TotalRevenue             float64   `json:"total_revenue"`
	ActiveSubscriptions      int       `json:"active_subscriptions"`
	CanceledSubscriptions    int       `json:"canceled_subscriptions"`
	PastDueSubscriptions     int       `json:"past_due_subscriptions"`
	TrialingSubscriptions    int       `json:"trialing_subscriptions"`
	TotalCustomers           int       `json:"total_customers"`
	ActiveCustomers          int       `json:"active_customers"`
	ChurnRate                float64   `json:"churn_rate"`
	ARPU                     float64   `json:"arpu"`
	TrialToPayConversionRate float64   `json:"trial_to_pay_conversion_rate"`
	LastUpdated              time.Time `json:"last_updated"`
}

// StripeCustomerInfo represents customer data from Stripe
type StripeCustomerInfo struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	Created       time.Time `json:"created"`
	MRR           float64   `json:"mrr"`
	Status        string    `json:"status"`
	Subscriptions int       `json:"subscriptions"`
}

// StripeSubscriptionInfo represents subscription data from Stripe
type StripeSubscriptionInfo struct {
	ID                 string    `json:"id"`
	CustomerID         string    `json:"customer_id"`
	CustomerEmail      string    `json:"customer_email"`
	Status             string    `json:"status"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	Amount             float64   `json:"amount"`
	Currency           string    `json:"currency"`
	Interval           string    `json:"interval"`
	ProductName        string    `json:"product_name"`
	Created            time.Time `json:"created"`
	CancelAtPeriodEnd  bool      `json:"cancel_at_period_end"`
}

// getStripeClient returns a Stripe client for the project
// SECURITY: Validates that the project belongs to the specified account
func (s *StripeService) getStripeClient(projectID, accountID string) (*client.API, error) {
	var project Project
	// SECURITY: Validate project belongs to the authenticated account
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Stripe access attempt - Account: %s, Project: %s - project not found or access denied", accountID, projectID)
		return nil, fmt.Errorf("project not found or access denied")
	}

	if project.StripeAPIKey == "" {
		return nil, fmt.Errorf("stripe API key not configured")
	}

	sc := &client.API{}
	sc.Init(project.StripeAPIKey, nil)
	return sc, nil
}

// UpdateStripeAPIKeyRequest represents the request to update Stripe API key
type UpdateStripeAPIKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

// UpdateStripeAPIKeyHandler updates the Stripe API key for a project
func (s *StripeService) UpdateStripeAPIKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// SECURITY: Validate project belongs to this account BEFORE any updates
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized Stripe API key update attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	var req UpdateStripeAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate Stripe API key format
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

	// Test the API key
	stripe.Key = req.ApiKey
	iter := customer.List(&stripe.CustomerListParams{
		ListParams: stripe.ListParams{Limit: stripe.Int64(1)},
	})
	if iter.Err() != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Stripe API key or insufficient permissions"})
		return
	}

	// Update project with Stripe API key - SECURITY: Already validated ownership above
	if err := s.db.Model(&Project{}).Where("id = ? AND account_id = ?", projectID, accountID.(string)).Update("stripe_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Stripe API key"})
		return
	}

	// Invalidate cache for this project
	s.cache.invalidateProject(projectID)

	c.JSON(http.StatusOK, gin.H{"message": "Stripe API key updated successfully"})
}

// GetRevenueMetricsHandler returns live revenue metrics from Stripe with time series data
func (s *StripeService) GetRevenueMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	days := 30 // Default to 30 days for time series

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache first
	cacheKey := projectID + ":metrics"
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("✅ Stripe metrics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	// Get Stripe client - SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  err.Error(),
			"data": map[string]interface{}{
				"configured": err.Error() != "stripe API key not configured",
			},
		})
		return
	}

	log.Printf("📡 Fetching live Stripe metrics for project %s", projectID)

	// Fetch live summary metrics from Stripe
	metrics, err := s.fetchLiveMetrics(sc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch Stripe data: %v", err),
		})
		return
	}

	// Fetch daily revenue for time series graph
	timeSeries, err := s.fetchDailyRevenue(sc, days)
	if err != nil {
		log.Printf("Warning: Failed to fetch daily revenue: %v", err)
		timeSeries = []map[string]interface{}{} // Empty array on error
	}

	// Construct response with both summary metrics and time series
	response := map[string]interface{}{
		"mrr":                          metrics.MRR,
		"arr":                          metrics.ARR,
		"total_revenue":                metrics.TotalRevenue,
		"active_subscriptions":         metrics.ActiveSubscriptions,
		"canceled_subscriptions":       metrics.CanceledSubscriptions,
		"past_due_subscriptions":       metrics.PastDueSubscriptions,
		"trialing_subscriptions":       metrics.TrialingSubscriptions,
		"total_customers":              metrics.TotalCustomers,
		"active_customers":             metrics.ActiveCustomers,
		"churn_rate":                   metrics.ChurnRate,
		"arpu":                         metrics.ARPU,
		"trial_to_pay_conversion_rate": metrics.TrialToPayConversionRate,
		"last_updated":                 metrics.LastUpdated,
		"time_series":                  timeSeries,
		"date_range": map[string]string{
			"start": time.Now().AddDate(0, 0, -days).Format("2006-01-02"),
			"end":   time.Now().Format("2006-01-02"),
		},
		// Additional fields for dashboard compatibility
		"growth_rate":           0.0, // Could be calculated with historical data
		"new_subscriptions":     0,   // Could be calculated from subscription creation dates
		"churned_subscriptions": metrics.CanceledSubscriptions,
	}

	// Cache the results
	s.cache.set(cacheKey, response, StripeCacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

// fetchLiveMetrics fetches and calculates metrics directly from Stripe API
func (s *StripeService) fetchLiveMetrics(sc *client.API) (*StripeMetrics, error) {
	metrics := &StripeMetrics{
		LastUpdated: time.Now(),
	}

	// Fetch all subscriptions
	subParams := &stripe.SubscriptionListParams{}
	subParams.Limit = stripe.Int64(100)

	var mrr int64 = 0
	activeCount := 0
	canceledCount := 0
	pastDueCount := 0
	trialingCount := 0
	activeCustomers := make(map[string]bool)

	subIter := sc.Subscriptions.List(subParams)
	for subIter.Next() {
		sub := subIter.Subscription()

		switch sub.Status {
		case stripe.SubscriptionStatusActive:
			activeCount++
			activeCustomers[sub.Customer.ID] = true

			// Calculate MRR from active subscriptions
			if len(sub.Items.Data) > 0 {
				item := sub.Items.Data[0]
				amount := item.Price.UnitAmount * item.Quantity

				// Normalize to monthly
				switch item.Price.Recurring.Interval {
				case stripe.PriceRecurringIntervalYear:
					amount = amount / 12
				case stripe.PriceRecurringIntervalWeek:
					amount = amount * 4
				case stripe.PriceRecurringIntervalDay:
					amount = amount * 30
				}
				mrr += amount
			}

		case stripe.SubscriptionStatusCanceled:
			canceledCount++

		case stripe.SubscriptionStatusPastDue:
			pastDueCount++
			activeCustomers[sub.Customer.ID] = true

		case stripe.SubscriptionStatusTrialing:
			trialingCount++
			activeCustomers[sub.Customer.ID] = true
		}
	}

	if err := subIter.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch subscriptions: %v", err)
	}

	// Fetch customer count
	customerParams := &stripe.CustomerListParams{}
	customerParams.Limit = stripe.Int64(100)

	totalCustomers := 0
	custIter := sc.Customers.List(customerParams)
	for custIter.Next() {
		totalCustomers++
	}

	if err := custIter.Err(); err != nil {
		return nil, fmt.Errorf("failed to fetch customers: %v", err)
	}

	// Fetch total revenue from charges (last 30 days)
	chargeParams := &stripe.ChargeListParams{}
	chargeParams.Limit = stripe.Int64(100)
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Unix()
	chargeParams.Created = &thirtyDaysAgo

	var totalRevenue int64 = 0
	chargeIter := sc.Charges.List(chargeParams)
	for chargeIter.Next() {
		charge := chargeIter.Charge()
		if charge.Paid && !charge.Refunded {
			totalRevenue += charge.Amount - charge.AmountRefunded
		}
	}

	if err := chargeIter.Err(); err != nil {
		log.Printf("Warning: Failed to fetch charges: %v", err)
		// Continue without charge data
	}

	// Calculate derived metrics
	metrics.MRR = float64(mrr) / 100
	metrics.ARR = metrics.MRR * 12
	metrics.TotalRevenue = float64(totalRevenue) / 100
	metrics.ActiveSubscriptions = activeCount
	metrics.CanceledSubscriptions = canceledCount
	metrics.PastDueSubscriptions = pastDueCount
	metrics.TrialingSubscriptions = trialingCount
	metrics.TotalCustomers = totalCustomers
	metrics.ActiveCustomers = len(activeCustomers)

	// Churn rate calculation
	totalSubs := activeCount + canceledCount
	if totalSubs > 0 {
		metrics.ChurnRate = float64(canceledCount) / float64(totalSubs) * 100
	}

	// ARPU calculation
	if metrics.ActiveCustomers > 0 {
		metrics.ARPU = metrics.MRR / float64(metrics.ActiveCustomers)
	}

	// Trial conversion rate
	convertedTrials := activeCount // Simplified: active subs that had trials
	totalTrials := trialingCount + convertedTrials
	if totalTrials > 0 {
		metrics.TrialToPayConversionRate = float64(convertedTrials) / float64(totalTrials) * 100
	}

	return metrics, nil
}

// GetCustomersHandler returns customer list from Stripe
func (s *StripeService) GetCustomersHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache
	cacheKey := projectID + ":customers"
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("✅ Stripe customers cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	// SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("📡 Fetching live Stripe customers for project %s", projectID)

	customers, err := s.fetchLiveCustomers(sc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch customers: %v", err),
		})
		return
	}

	// Cache results
	s.cache.set(cacheKey, customers, StripeCustomersTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": map[string]interface{}{
			"customers":       customers,
			"total_customers": len(customers),
		},
		"cached": false,
	})
}

// fetchLiveCustomers fetches customers and their subscription info from Stripe
func (s *StripeService) fetchLiveCustomers(sc *client.API) ([]StripeCustomerInfo, error) {
	var customers []StripeCustomerInfo

	// Fetch all subscriptions first to calculate MRR per customer
	customerMRR := make(map[string]float64)
	customerSubCount := make(map[string]int)
	customerStatus := make(map[string]string)

	subParams := &stripe.SubscriptionListParams{}
	subParams.Limit = stripe.Int64(100)

	subIter := sc.Subscriptions.List(subParams)
	for subIter.Next() {
		sub := subIter.Subscription()
		custID := sub.Customer.ID

		if sub.Status == stripe.SubscriptionStatusActive {
			customerStatus[custID] = "active"
			customerSubCount[custID]++

			if len(sub.Items.Data) > 0 {
				item := sub.Items.Data[0]
				amount := float64(item.Price.UnitAmount*item.Quantity) / 100

				// Normalize to monthly
				switch item.Price.Recurring.Interval {
				case stripe.PriceRecurringIntervalYear:
					amount = amount / 12
				case stripe.PriceRecurringIntervalWeek:
					amount = amount * 4
				case stripe.PriceRecurringIntervalDay:
					amount = amount * 30
				}
				customerMRR[custID] += amount
			}
		} else if customerStatus[custID] == "" {
			customerStatus[custID] = string(sub.Status)
		}
	}

	if err := subIter.Err(); err != nil {
		return nil, err
	}

	// Fetch customers
	custParams := &stripe.CustomerListParams{}
	custParams.Limit = stripe.Int64(100)

	custIter := sc.Customers.List(custParams)
	for custIter.Next() {
		cust := custIter.Customer()

		status := customerStatus[cust.ID]
		if status == "" {
			status = "free"
		}

		customers = append(customers, StripeCustomerInfo{
			ID:            cust.ID,
			Email:         cust.Email,
			Name:          cust.Name,
			Created:       time.Unix(cust.Created, 0),
			MRR:           customerMRR[cust.ID],
			Status:        status,
			Subscriptions: customerSubCount[cust.ID],
		})
	}

	if err := custIter.Err(); err != nil {
		return nil, err
	}

	return customers, nil
}

// GetSubscriptionsHandler returns subscription list from Stripe
func (s *StripeService) GetSubscriptionsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	status := c.DefaultQuery("status", "") // Optional filter

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache
	cacheKey := projectID + ":subscriptions:" + status
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("✅ Stripe subscriptions cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	// SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("📡 Fetching live Stripe subscriptions for project %s", projectID)

	subscriptions, err := s.fetchLiveSubscriptions(sc, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch subscriptions: %v", err),
		})
		return
	}

	// Cache results
	s.cache.set(cacheKey, subscriptions, StripeSubscriptionTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": map[string]interface{}{
			"subscriptions":       subscriptions,
			"total_subscriptions": len(subscriptions),
		},
		"cached": false,
	})
}

// fetchLiveSubscriptions fetches subscriptions from Stripe
func (s *StripeService) fetchLiveSubscriptions(sc *client.API, statusFilter string) ([]StripeSubscriptionInfo, error) {
	var subscriptions []StripeSubscriptionInfo

	params := &stripe.SubscriptionListParams{}
	params.Limit = stripe.Int64(100)

	if statusFilter != "" {
		params.Status = statusFilter
	}

	iter := sc.Subscriptions.List(params)
	for iter.Next() {
		sub := iter.Subscription()

		var amount float64
		var currency, interval, productName string

		if len(sub.Items.Data) > 0 {
			item := sub.Items.Data[0]
			amount = float64(item.Price.UnitAmount*item.Quantity) / 100
			currency = string(item.Price.Currency)
			interval = string(item.Price.Recurring.Interval)

			if item.Price.Product != nil {
				productName = item.Price.Product.Name
			}
		}

		subscriptions = append(subscriptions, StripeSubscriptionInfo{
			ID:                 sub.ID,
			CustomerID:         sub.Customer.ID,
			CustomerEmail:      "", // Would need additional API call to get
			Status:             string(sub.Status),
			CurrentPeriodStart: time.Unix(sub.CurrentPeriodStart, 0),
			CurrentPeriodEnd:   time.Unix(sub.CurrentPeriodEnd, 0),
			Amount:             amount,
			Currency:           currency,
			Interval:           interval,
			ProductName:        productName,
			Created:            time.Unix(sub.Created, 0),
			CancelAtPeriodEnd:  sub.CancelAtPeriodEnd,
		})
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	return subscriptions, nil
}

// GetRevenueAnalyticsHandler returns revenue analytics with time series
func (s *StripeService) GetRevenueAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	days := 30 // Default to 30 days

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache
	cacheKey := fmt.Sprintf("%s:analytics:%d", projectID, days)
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("✅ Stripe analytics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	// SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("📡 Fetching live Stripe analytics for project %s", projectID)

	// Get current metrics
	metrics, err := s.fetchLiveMetrics(sc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch metrics: %v", err),
		})
		return
	}

	// Fetch daily revenue from charges
	timeSeries, err := s.fetchDailyRevenue(sc, days)
	if err != nil {
		log.Printf("Warning: Failed to fetch daily revenue: %v", err)
		timeSeries = []map[string]interface{}{} // Empty array on error
	}

	response := map[string]interface{}{
		"summary":     metrics,
		"time_series": timeSeries,
		"date_range": map[string]string{
			"start": time.Now().AddDate(0, 0, -days).Format("2006-01-02"),
			"end":   time.Now().Format("2006-01-02"),
		},
	}

	// Cache results
	s.cache.set(cacheKey, response, StripeCacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

// fetchDailyRevenue fetches daily revenue from charges
func (s *StripeService) fetchDailyRevenue(sc *client.API, days int) ([]map[string]interface{}, error) {
	startDate := time.Now().AddDate(0, 0, -days)

	// Group revenue by day
	dailyRevenue := make(map[string]int64)

	params := &stripe.ChargeListParams{}
	params.Limit = stripe.Int64(100)
	created := startDate.Unix()
	params.Created = &created

	iter := sc.Charges.List(params)
	for iter.Next() {
		charge := iter.Charge()
		if charge.Paid && !charge.Refunded {
			day := time.Unix(charge.Created, 0).Format("2006-01-02")
			dailyRevenue[day] += charge.Amount - charge.AmountRefunded
		}
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	// Convert to sorted time series
	var timeSeries []map[string]interface{}
	for i := 0; i < days; i++ {
		day := startDate.AddDate(0, 0, i).Format("2006-01-02")
		revenue := float64(dailyRevenue[day]) / 100

		timeSeries = append(timeSeries, map[string]interface{}{
			"date":    day,
			"revenue": revenue,
		})
	}

	return timeSeries, nil
}

// GetCustomerAnalyticsHandler returns customer-focused analytics
func (s *StripeService) GetCustomerAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache
	cacheKey := projectID + ":customer_analytics"
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("✅ Stripe customer analytics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	// SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("📡 Fetching live customer analytics for project %s", projectID)

	customers, err := s.fetchLiveCustomers(sc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch customers: %v", err),
		})
		return
	}

	// Calculate analytics
	totalCustomers := len(customers)
	paidCustomers := 0
	totalMRR := 0.0

	for _, cust := range customers {
		if cust.MRR > 0 {
			paidCustomers++
			totalMRR += cust.MRR
		}
	}

	freeCustomers := totalCustomers - paidCustomers
	conversionRate := 0.0
	if totalCustomers > 0 {
		conversionRate = float64(paidCustomers) / float64(totalCustomers) * 100
	}

	avgMRR := 0.0
	if paidCustomers > 0 {
		avgMRR = totalMRR / float64(paidCustomers)
	}

	response := map[string]interface{}{
		"summary": map[string]interface{}{
			"total_customers": totalCustomers,
			"paid_customers":  paidCustomers,
			"free_customers":  freeCustomers,
			"total_mrr":       totalMRR,
			"avg_mrr":         avgMRR,
			"conversion_rate": conversionRate,
		},
		"customers": customers,
	}

	// Cache results
	s.cache.set(cacheKey, response, StripeCustomersTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

// RefreshCacheHandler forces a cache refresh for Stripe data
func (s *StripeService) RefreshCacheHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// SECURITY: Validate project belongs to this account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized cache refresh attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	// Invalidate all caches for this project
	s.cache.invalidateProject(projectID)

	log.Printf("🗑️ Cleared Stripe cache for project %s", projectID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Stripe cache cleared. Next request will fetch fresh data.",
	})
}

// SyncStripeDataHandler - Legacy handler, now just verifies connection and clears cache
func (s *StripeService) SyncStripeDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// SECURITY: Get and validate account ID
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// SECURITY: validates project ownership
	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Test connection by fetching one customer
	params := &stripe.CustomerListParams{}
	params.Limit = stripe.Int64(1)
	iter := sc.Customers.List(params)
	if iter.Err() != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Failed to connect to Stripe. Please check your API key.",
		})
		return
	}

	// Clear cache to force fresh data
	s.cache.invalidateProject(projectID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Stripe connection verified. Data is now fetched live from Stripe API with caching.",
		"note":    "Manual sync is no longer needed. Metrics are calculated in real-time.",
	})
}
