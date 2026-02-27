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
	db               *gorm.DB
	cache            *StripeCache
	projectListCache *BoundedCache[[]Project]
	responseCache    *BoundedCache[*CachedResponse]
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

// invalidateAllCaches clears all cached data for a project across all cache layers
func (s *StripeService) invalidateAllCaches(projectID, accountID string) {
	// 1. Invalidate Stripe service cache
	s.cache.invalidateProject(projectID)

	// 2. Invalidate HTTP response cache for stripe endpoints
	if s.responseCache != nil {
		// Response cache keys use format: resp:/api/v1/projects/<projectID>/stripe/...:projectID:accountID:...
		prefix := fmt.Sprintf("resp:/api/v1/projects/%s/stripe/", projectID)
		s.responseCache.DeleteByPrefix(prefix)
	}

	// 3. Invalidate project list cache so frontend picks up hasStripeKey change
	if s.projectListCache != nil {
		cacheKey := fmt.Sprintf("account_projects:%s", accountID)
		s.projectListCache.Delete(cacheKey)
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

	// Invalidate ALL cache layers for this project
	s.invalidateAllCaches(projectID, accountID.(string))

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

	// Fetch complete time series
	timeSeries, err := s.fetchTimeSeries(sc, days)
	if err != nil {
		log.Printf("Warning: Failed to fetch time series: %v", err)
		timeSeries = []map[string]interface{}{}
	}

	// Count new subscriptions in the period by checking subscription creation dates
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	newSubs := 0
	subParams := &stripe.SubscriptionListParams{}
	subParams.Limit = stripe.Int64(100)
	subIter := sc.Subscriptions.List(subParams)
	for subIter.Next() {
		sub := subIter.Subscription()
		createdDay := time.Unix(sub.Created, 0).Format("2006-01-02")
		if createdDay >= thirtyDaysAgo {
			newSubs++
		}
	}

	// Construct response with all fields the frontend expects
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
		"growth_rate":           0.0,
		"new_subscriptions":     newSubs,
		"churned_subscriptions": metrics.CanceledSubscriptions,
		"expansion_revenue":     0.0,
		"contraction_revenue":   0.0,
		"net_revenue":           metrics.TotalRevenue,
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

		custID := ""
		if sub.Customer != nil {
			custID = sub.Customer.ID
		}

		switch sub.Status {
		case stripe.SubscriptionStatusActive:
			activeCount++
			if custID != "" {
				activeCustomers[custID] = true
			}

			// Calculate MRR from active subscriptions
			if len(sub.Items.Data) > 0 {
				item := sub.Items.Data[0]
				if item.Price != nil {
					amount := item.Price.UnitAmount * item.Quantity

					// Normalize to monthly
					if item.Price.Recurring != nil {
						switch item.Price.Recurring.Interval {
						case stripe.PriceRecurringIntervalYear:
							amount = amount / 12
						case stripe.PriceRecurringIntervalWeek:
							amount = amount * 4
						case stripe.PriceRecurringIntervalDay:
							amount = amount * 30
						}
					}
					mrr += amount
				}
			}

		case stripe.SubscriptionStatusCanceled:
			canceledCount++

		case stripe.SubscriptionStatusPastDue:
			pastDueCount++
			if custID != "" {
				activeCustomers[custID] = true
			}

		case stripe.SubscriptionStatusTrialing:
			trialingCount++
			if custID != "" {
				activeCustomers[custID] = true
			}
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
	chargeParams.CreatedRange = &stripe.RangeQueryParams{GreaterThanOrEqual: thirtyDaysAgo}

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
		custID := ""
		if sub.Customer != nil {
			custID = sub.Customer.ID
		}
		if custID == "" {
			continue
		}

		if sub.Status == stripe.SubscriptionStatusActive {
			customerStatus[custID] = "active"
			customerSubCount[custID]++

			if len(sub.Items.Data) > 0 {
				item := sub.Items.Data[0]
				if item.Price != nil {
					amount := float64(item.Price.UnitAmount*item.Quantity) / 100

					// Normalize to monthly
					if item.Price.Recurring != nil {
						switch item.Price.Recurring.Interval {
						case stripe.PriceRecurringIntervalYear:
							amount = amount / 12
						case stripe.PriceRecurringIntervalWeek:
							amount = amount * 4
						case stripe.PriceRecurringIntervalDay:
							amount = amount * 30
						}
					}
					customerMRR[custID] += amount
				}
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
			if item.Price != nil {
				amount = float64(item.Price.UnitAmount*item.Quantity) / 100
				currency = string(item.Price.Currency)
				if item.Price.Recurring != nil {
					interval = string(item.Price.Recurring.Interval)
				}
				if item.Price.Product != nil {
					productName = item.Price.Product.Name
				}
			}
		}

		custID := ""
		if sub.Customer != nil {
			custID = sub.Customer.ID
		}

		subscriptions = append(subscriptions, StripeSubscriptionInfo{
			ID:                 sub.ID,
			CustomerID:         custID,
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

	// Fetch complete time series with all fields the dashboard needs
	timeSeries, err := s.fetchTimeSeries(sc, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch analytics: %v", err),
		})
		return
	}

	// Also get current summary metrics
	metrics, err := s.fetchLiveMetrics(sc)
	if err != nil {
		log.Printf("Warning: Failed to fetch summary metrics: %v", err)
	}

	summary := map[string]interface{}{}
	if metrics != nil {
		summary = map[string]interface{}{
			"current_mrr":            metrics.MRR,
			"current_arr":            metrics.ARR,
			"active_subscriptions":   metrics.ActiveSubscriptions,
			"churn_rate":             metrics.ChurnRate,
			"arpu":                   metrics.ARPU,
			"trial_conversion_rate":  metrics.TrialToPayConversionRate,
		}
	}

	response := map[string]interface{}{
		"summary":     summary,
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

// fetchTimeSeries builds a complete daily time series with mrr, revenue,
// active_subscriptions, churn_rate, and arpu from live Stripe data.
func (s *StripeService) fetchTimeSeries(sc *client.API, days int) ([]map[string]interface{}, error) {
	startDate := time.Now().AddDate(0, 0, -days)
	startUnix := startDate.Unix()

	// ── 1. Daily revenue from charges ──
	dailyRevenue := make(map[string]int64)
	chargeParams := &stripe.ChargeListParams{}
	chargeParams.Limit = stripe.Int64(100)
	chargeParams.CreatedRange = &stripe.RangeQueryParams{GreaterThanOrEqual: startUnix}

	chargeIter := sc.Charges.List(chargeParams)
	for chargeIter.Next() {
		ch := chargeIter.Charge()
		if ch.Paid && !ch.Refunded {
			day := time.Unix(ch.Created, 0).Format("2006-01-02")
			dailyRevenue[day] += ch.Amount - ch.AmountRefunded
		}
	}
	if err := chargeIter.Err(); err != nil {
		log.Printf("Warning: failed to fetch charges for time series: %v", err)
	}

	// ── 2. Subscription events: created / canceled dates ──
	type subEvent struct {
		createdDay   string
		canceledDay  string
		monthlyAmt   int64 // in cents, normalized to monthly
		isActive     bool
		isCanceled   bool
	}
	var subs []subEvent

	subParams := &stripe.SubscriptionListParams{Status: "all"}
	subParams.Limit = stripe.Int64(100)
	subIter := sc.Subscriptions.List(subParams)
	for subIter.Next() {
		sub := subIter.Subscription()

		var monthlyAmt int64
		if len(sub.Items.Data) > 0 {
			item := sub.Items.Data[0]
			if item.Price != nil {
				amount := item.Price.UnitAmount * item.Quantity
				if item.Price.Recurring != nil {
					switch item.Price.Recurring.Interval {
					case stripe.PriceRecurringIntervalYear:
						amount = amount / 12
					case stripe.PriceRecurringIntervalWeek:
						amount = amount * 4
					case stripe.PriceRecurringIntervalDay:
						amount = amount * 30
					}
				}
				monthlyAmt = amount
			}
		}

		ev := subEvent{
			createdDay: time.Unix(sub.Created, 0).Format("2006-01-02"),
			monthlyAmt: monthlyAmt,
			isActive:   sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing,
			isCanceled: sub.Status == stripe.SubscriptionStatusCanceled,
		}
		if sub.CanceledAt > 0 {
			ev.canceledDay = time.Unix(sub.CanceledAt, 0).Format("2006-01-02")
		}
		subs = append(subs, ev)
	}
	if err := subIter.Err(); err != nil {
		log.Printf("Warning: failed to fetch subscriptions for time series: %v", err)
	}

	// ── 3. Customer count by creation date ──
	dailyNewCustomers := make(map[string]int)
	custParams := &stripe.CustomerListParams{}
	custParams.Limit = stripe.Int64(100)
	custIter := sc.Customers.List(custParams)
	for custIter.Next() {
		cust := custIter.Customer()
		day := time.Unix(cust.Created, 0).Format("2006-01-02")
		dailyNewCustomers[day]++
	}
	if err := custIter.Err(); err != nil {
		log.Printf("Warning: failed to fetch customers for time series: %v", err)
	}

	// ── 4. Build daily time series ──
	// Sort all customer creation dates to compute cumulative count
	var allCustomerDays []string
	for day := range dailyNewCustomers {
		allCustomerDays = append(allCustomerDays, day)
	}

	// Count customers created before the time window
	customersBeforeWindow := 0
	windowStart := startDate.Format("2006-01-02")
	custParams2 := &stripe.CustomerListParams{}
	custParams2.Limit = stripe.Int64(100)
	custIter2 := sc.Customers.List(custParams2)
	for custIter2.Next() {
		cust := custIter2.Customer()
		day := time.Unix(cust.Created, 0).Format("2006-01-02")
		if day < windowStart {
			customersBeforeWindow++
		}
	}

	cumulativeCustomers := customersBeforeWindow

	var timeSeries []map[string]interface{}
	for i := 0; i <= days; i++ { // include today
		day := startDate.AddDate(0, 0, i).Format("2006-01-02")

		// Accumulate customers
		cumulativeCustomers += dailyNewCustomers[day]

		// Count active subs and MRR as of end of this day
		activeSubs := 0
		canceledOnDay := 0
		var mrrCents int64
		for _, sub := range subs {
			createdBefore := sub.createdDay <= day
			canceledAfter := sub.canceledDay == "" || sub.canceledDay > day
			if createdBefore && canceledAfter {
				activeSubs++
				mrrCents += sub.monthlyAmt
			}
			if sub.canceledDay == day {
				canceledOnDay++
			}
		}

		mrr := float64(mrrCents) / 100
		revenue := float64(dailyRevenue[day]) / 100

		churnRate := 0.0
		if activeSubs+canceledOnDay > 0 {
			churnRate = float64(canceledOnDay) / float64(activeSubs+canceledOnDay) * 100
		}

		arpu := 0.0
		if activeSubs > 0 {
			arpu = mrr / float64(activeSubs)
		}

		timeSeries = append(timeSeries, map[string]interface{}{
			"date":                 day,
			"mrr":                  mrr,
			"arr":                  mrr * 12,
			"revenue":              revenue,
			"active_subscriptions": activeSubs,
			"churn_rate":           churnRate,
			"arpu":                 arpu,
			"new_customers":        dailyNewCustomers[day],
			"total_customers":      cumulativeCustomers,
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

	// Invalidate ALL cache layers for this project
	s.invalidateAllCaches(projectID, accountID.(string))

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

	// Clear ALL cache layers to force fresh data
	s.invalidateAllCaches(projectID, accountID.(string))

	c.JSON(http.StatusOK, gin.H{
		"message": "Stripe connection verified. Data is now fetched live from Stripe API with caching.",
		"note":    "Manual sync is no longer needed. Metrics are calculated in real-time.",
	})
}

// TestStripeTimeSeriesHandler is a debug endpoint that returns raw data from Stripe (no cache).
func (s *StripeService) TestStripeTimeSeriesHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	sc, err := s.getStripeClient(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Raw subscription dump (all statuses)
	var rawSubs []map[string]interface{}

	// Fetch active subscriptions
	subParams := &stripe.SubscriptionListParams{}
	subParams.Limit = stripe.Int64(100)
	subIter := sc.Subscriptions.List(subParams)
	for subIter.Next() {
		sub := subIter.Subscription()
		var amount int64
		var interval string
		if len(sub.Items.Data) > 0 {
			item := sub.Items.Data[0]
			if item.Price != nil {
				amount = item.Price.UnitAmount * item.Quantity
				if item.Price.Recurring != nil {
					interval = string(item.Price.Recurring.Interval)
				}
			}
		}
		custID := ""
		if sub.Customer != nil {
			custID = sub.Customer.ID
		}
		rawSubs = append(rawSubs, map[string]interface{}{
			"id":         sub.ID,
			"status":     string(sub.Status),
			"created":    time.Unix(sub.Created, 0).Format(time.RFC3339),
			"amount":     amount,
			"interval":   interval,
			"customer":   custID,
			"canceled_at": sub.CanceledAt,
		})
	}
	subErr := ""
	if subIter.Err() != nil {
		subErr = subIter.Err().Error()
	}

	// Also fetch canceled/all subscriptions
	subParamsAll := &stripe.SubscriptionListParams{Status: "all"}
	subParamsAll.Limit = stripe.Int64(100)
	subIterAll := sc.Subscriptions.List(subParamsAll)
	var allSubCount int
	for subIterAll.Next() {
		allSubCount++
		sub := subIterAll.Subscription()
		// Only add if not already in rawSubs (i.e., non-active)
		if sub.Status != stripe.SubscriptionStatusActive {
			var amount int64
			var interval string
			if len(sub.Items.Data) > 0 {
				item := sub.Items.Data[0]
				if item.Price != nil {
					amount = item.Price.UnitAmount * item.Quantity
					if item.Price.Recurring != nil {
						interval = string(item.Price.Recurring.Interval)
					}
				}
			}
			custID := ""
			if sub.Customer != nil {
				custID = sub.Customer.ID
			}
			rawSubs = append(rawSubs, map[string]interface{}{
				"id":          sub.ID,
				"status":      string(sub.Status),
				"created":     time.Unix(sub.Created, 0).Format(time.RFC3339),
				"amount":      amount,
				"interval":    interval,
				"customer":    custID,
				"canceled_at": sub.CanceledAt,
			})
		}
	}
	allSubErr := ""
	if subIterAll.Err() != nil {
		allSubErr = subIterAll.Err().Error()
	}

	// Raw charge dump
	var rawCharges []map[string]interface{}
	chargeParams := &stripe.ChargeListParams{}
	chargeParams.Limit = stripe.Int64(100)
	chargeIter := sc.Charges.List(chargeParams)
	for chargeIter.Next() {
		ch := chargeIter.Charge()
		rawCharges = append(rawCharges, map[string]interface{}{
			"id":       ch.ID,
			"amount":   ch.Amount,
			"paid":     ch.Paid,
			"refunded": ch.Refunded,
			"created":  time.Unix(ch.Created, 0).Format(time.RFC3339),
			"customer": func() string { if ch.Customer != nil { return ch.Customer.ID }; return "" }(),
		})
	}
	chargeErr := ""
	if chargeIter.Err() != nil {
		chargeErr = chargeIter.Err().Error()
	}

	timeSeries, tsErr := s.fetchTimeSeries(sc, 30)
	tsErrStr := ""
	if tsErr != nil {
		tsErrStr = tsErr.Error()
	}

	c.JSON(http.StatusOK, gin.H{
		"raw_subscriptions":           rawSubs,
		"raw_subscriptions_count":     len(rawSubs),
		"raw_subscriptions_error":     subErr,
		"all_subscriptions_count":     allSubCount,
		"all_subscriptions_error":     allSubErr,
		"raw_charges":                 rawCharges,
		"raw_charges_count":           len(rawCharges),
		"raw_charges_error":           chargeErr,
		"time_series_count":           len(timeSeries),
		"time_series":                 timeSeries,
		"time_series_error":           tsErrStr,
	})
}
