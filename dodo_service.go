package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DodoService handles all DodoPayments-related operations with live data fetching
type DodoService struct {
	db               *gorm.DB
	cache            *DodoCache
	projectListCache *BoundedCache[[]Project]
	responseCache    *BoundedCache[*CachedResponse]
	httpClient       *http.Client
}

const (
	DodoBaseURLLive = "https://api.dodopayments.com"
	DodoBaseURLTest = "https://api.dodopayments.com" // same base, mode set via key

	DodoCacheTTL        = 5 * time.Minute
	DodoCustomersTTL    = 10 * time.Minute
	DodoSubscriptionTTL = 5 * time.Minute
)

// DodoCache provides in-memory caching for Dodo data
type DodoCache struct {
	mu      sync.RWMutex
	entries map[string]*DodoCacheEntry
}

// DodoCacheEntry represents a cached Dodo data entry
type DodoCacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

func (c *DodoCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Data, true
}

func (c *DodoCache) set(key string, data interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &DodoCacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (c *DodoCache) invalidateProject(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if len(key) > len(projectID) && key[:len(projectID)] == projectID {
			delete(c.entries, key)
		}
	}
}

// NewDodoService creates a new DodoService instance
func NewDodoService(db *gorm.DB) *DodoService {
	return &DodoService{
		db: db,
		cache: &DodoCache{
			entries: make(map[string]*DodoCacheEntry),
		},
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *DodoService) invalidateAllCaches(projectID, accountID string) {
	s.cache.invalidateProject(projectID)

	if s.responseCache != nil {
		prefix := fmt.Sprintf("resp:/api/v1/projects/%s/dodo/", projectID)
		s.responseCache.DeleteByPrefix(prefix)
	}

	if s.projectListCache != nil {
		cacheKey := fmt.Sprintf("account_projects:%s", accountID)
		s.projectListCache.Delete(cacheKey)
	}
}

// getDodoAPIKey retrieves and validates the Dodo API key for a project
func (s *DodoService) getDodoAPIKey(projectID, accountID string) (string, error) {
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		log.Printf("SECURITY: Dodo access attempt - Account: %s, Project: %s - project not found or access denied", accountID, projectID)
		return "", fmt.Errorf("project not found or access denied")
	}

	if project.DodoAPIKey == "" {
		return "", fmt.Errorf("dodo payments API key not configured")
	}

	return project.DodoAPIKey, nil
}

// dodoRequest makes an authenticated request to the DodoPayments API
func (s *DodoService) dodoRequest(method, path, apiKey string) ([]byte, error) {
	url := DodoBaseURLLive + path

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// DodoPayment represents a payment from DodoPayments API
type DodoPayment struct {
	PaymentID     string                 `json:"payment_id"`
	BusinessID    string                 `json:"business_id"`
	CustomerID    string                 `json:"customer_id"`
	TotalAmount   int64                  `json:"total_amount"`
	Currency      string                 `json:"currency"`
	Status        string                 `json:"status"`
	CreatedAt     string                 `json:"created_at"`
	UpdatedAt     string                 `json:"updated_at"`
	RefundStatus  *string                `json:"refund_status"`
	DisputeStatus *string                `json:"dispute_status"`
	ProductCart   []map[string]interface{} `json:"product_cart"`
	Metadata      map[string]string      `json:"metadata"`
	PaymentLink   string                 `json:"payment_link"`
	Customer      DodoCustomerRef        `json:"customer"`
}

type DodoCustomerRef struct {
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
}

// DodoSubscription represents a subscription from DodoPayments API
type DodoSubscription struct {
	SubscriptionID  string            `json:"subscription_id"`
	BusinessID      string            `json:"business_id"`
	CustomerID      string            `json:"customer_id"`
	ProductID       string            `json:"product_id"`
	Status          string            `json:"status"`
	Currency        string            `json:"currency"`
	RecurringAmount int64             `json:"recurring_amount"`
	Quantity        int64             `json:"quantity"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	NextBillingDate string            `json:"next_billing_date"`
	PreviousBillingDate string        `json:"previous_billing_date"`
	TrialPeriodDays int               `json:"trial_period_days"`
	Metadata        map[string]string `json:"metadata"`
	Customer        DodoCustomerRef   `json:"customer"`
}

// DodoCustomer represents a customer from DodoPayments API
type DodoCustomer struct {
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	CreatedAt  string `json:"created_at"`
	BusinessID string `json:"business_id"`
}

// DodoRefund represents a refund from DodoPayments API
type DodoRefund struct {
	RefundID  string `json:"refund_id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reason    string `json:"reason"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// DodoListResponse represents a paginated list response from DodoPayments API
type DodoListResponse struct {
	Items    json.RawMessage `json:"items"`
	PageSize int             `json:"page_size"`
	PageNumber int           `json:"page_number"`
}

// ---- API Key Management ----

type UpdateDodoAPIKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

func (s *DodoService) UpdateDodoAPIKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("SECURITY: Unauthorized Dodo API key update attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	var req UpdateDodoAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.ApiKey) < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key format"})
		return
	}

	// Test the API key by fetching customers (lightweight call)
	testReq, err := http.NewRequest("GET", DodoBaseURLLive+"/customers?page_size=1", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate API key"})
		return
	}
	testReq.Header.Set("Authorization", "Bearer "+req.ApiKey)
	testReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(testReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to connect to DodoPayments. Please check your API key."})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid DodoPayments API key or insufficient permissions"})
		return
	}

	if err := s.db.Model(&Project{}).Where("id = ? AND account_id = ?", projectID, accountID.(string)).Update("dodo_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update DodoPayments API key"})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	c.JSON(http.StatusOK, gin.H{"message": "DodoPayments API key updated successfully"})
}

// ---- Data Fetching ----

func (s *DodoService) fetchPayments(apiKey string) ([]DodoPayment, error) {
	var allPayments []DodoPayment
	pageNumber := 0

	for {
		path := fmt.Sprintf("/payments?page_size=100&page_number=%d", pageNumber)
		body, err := s.dodoRequest("GET", path, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch payments: %v", err)
		}

		var payments []DodoPayment
		if err := json.Unmarshal(body, &payments); err != nil {
			// Try parsing as paginated response
			var listResp DodoListResponse
			if err2 := json.Unmarshal(body, &listResp); err2 != nil {
				return nil, fmt.Errorf("failed to parse payments response: %v", err)
			}
			if err := json.Unmarshal(listResp.Items, &payments); err != nil {
				return nil, fmt.Errorf("failed to parse payment items: %v", err)
			}
		}

		if len(payments) == 0 {
			break
		}

		allPayments = append(allPayments, payments...)

		if len(payments) < 100 {
			break
		}
		pageNumber++

		// Safety limit
		if pageNumber > 10 {
			break
		}
	}

	return allPayments, nil
}

func (s *DodoService) fetchSubscriptions(apiKey string) ([]DodoSubscription, error) {
	var allSubs []DodoSubscription
	pageNumber := 0

	for {
		path := fmt.Sprintf("/subscriptions?page_size=100&page_number=%d", pageNumber)
		body, err := s.dodoRequest("GET", path, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch subscriptions: %v", err)
		}

		var subs []DodoSubscription
		if err := json.Unmarshal(body, &subs); err != nil {
			var listResp DodoListResponse
			if err2 := json.Unmarshal(body, &listResp); err2 != nil {
				return nil, fmt.Errorf("failed to parse subscriptions response: %v", err)
			}
			if err := json.Unmarshal(listResp.Items, &subs); err != nil {
				return nil, fmt.Errorf("failed to parse subscription items: %v", err)
			}
		}

		if len(subs) == 0 {
			break
		}

		allSubs = append(allSubs, subs...)

		if len(subs) < 100 {
			break
		}
		pageNumber++

		if pageNumber > 10 {
			break
		}
	}

	return allSubs, nil
}

func (s *DodoService) fetchCustomers(apiKey string) ([]DodoCustomer, error) {
	var allCustomers []DodoCustomer
	pageNumber := 0

	for {
		path := fmt.Sprintf("/customers?page_size=100&page_number=%d", pageNumber)
		body, err := s.dodoRequest("GET", path, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch customers: %v", err)
		}

		var customers []DodoCustomer
		if err := json.Unmarshal(body, &customers); err != nil {
			var listResp DodoListResponse
			if err2 := json.Unmarshal(body, &listResp); err2 != nil {
				return nil, fmt.Errorf("failed to parse customers response: %v", err)
			}
			if err := json.Unmarshal(listResp.Items, &customers); err != nil {
				return nil, fmt.Errorf("failed to parse customer items: %v", err)
			}
		}

		if len(customers) == 0 {
			break
		}

		allCustomers = append(allCustomers, customers...)

		if len(customers) < 100 {
			break
		}
		pageNumber++

		if pageNumber > 10 {
			break
		}
	}

	return allCustomers, nil
}

func (s *DodoService) fetchRefunds(apiKey string) ([]DodoRefund, error) {
	var allRefunds []DodoRefund
	pageNumber := 0

	for {
		path := fmt.Sprintf("/refunds?page_size=100&page_number=%d", pageNumber)
		body, err := s.dodoRequest("GET", path, apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch refunds: %v", err)
		}

		var refunds []DodoRefund
		if err := json.Unmarshal(body, &refunds); err != nil {
			var listResp DodoListResponse
			if err2 := json.Unmarshal(body, &listResp); err2 != nil {
				return nil, fmt.Errorf("failed to parse refunds response: %v", err)
			}
			if err := json.Unmarshal(listResp.Items, &refunds); err != nil {
				return nil, fmt.Errorf("failed to parse refund items: %v", err)
			}
		}

		if len(refunds) == 0 {
			break
		}

		allRefunds = append(allRefunds, refunds...)

		if len(refunds) < 100 {
			break
		}
		pageNumber++

		if pageNumber > 10 {
			break
		}
	}

	return allRefunds, nil
}

// ---- Metrics Calculation ----

func parseTime(s string) time.Time {
	// Try multiple formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// DodoMetrics represents calculated revenue metrics from DodoPayments data
type DodoMetrics struct {
	MRR                   float64   `json:"mrr"`
	ARR                   float64   `json:"arr"`
	TotalRevenue          float64   `json:"total_revenue"`
	ActiveSubscriptions   int       `json:"active_subscriptions"`
	CanceledSubscriptions int       `json:"canceled_subscriptions"`
	OnHoldSubscriptions   int       `json:"on_hold_subscriptions"`
	ExpiredSubscriptions  int       `json:"expired_subscriptions"`
	TotalCustomers        int       `json:"total_customers"`
	ActiveCustomers       int       `json:"active_customers"`
	ChurnRate             float64   `json:"churn_rate"`
	ARPU                  float64   `json:"arpu"`
	ChurnedMRR            float64   `json:"churned_mrr"`
	RefundedAmount        float64   `json:"refunded_amount"`
	DisputeCount          int       `json:"dispute_count"`
	LastUpdated           time.Time `json:"last_updated"`
}

func (s *DodoService) calculateMetrics(payments []DodoPayment, subscriptions []DodoSubscription, customers []DodoCustomer, refunds []DodoRefund) *DodoMetrics {
	metrics := &DodoMetrics{
		LastUpdated:    time.Now(),
		TotalCustomers: len(customers),
	}

	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	activeCustomers := make(map[string]bool)

	// Calculate subscription metrics
	var mrrCents int64
	for _, sub := range subscriptions {
		switch sub.Status {
		case "active":
			metrics.ActiveSubscriptions++
			if sub.CustomerID != "" {
				activeCustomers[sub.CustomerID] = true
			}
			// Add to MRR (recurring_amount is already the periodic amount)
			mrrCents += sub.RecurringAmount
		case "cancelled":
			metrics.CanceledSubscriptions++
		case "on_hold":
			metrics.OnHoldSubscriptions++
			if sub.CustomerID != "" {
				activeCustomers[sub.CustomerID] = true
			}
		case "expired":
			metrics.ExpiredSubscriptions++
		}
	}

	metrics.MRR = float64(mrrCents) / 100
	metrics.ARR = metrics.MRR * 12
	metrics.ActiveCustomers = len(activeCustomers)

	// Calculate total revenue from successful payments in last 30 days
	var totalRevenueCents int64
	for _, payment := range payments {
		if payment.Status == "succeeded" {
			createdAt := parseTime(payment.CreatedAt)
			if createdAt.After(thirtyDaysAgo) {
				totalRevenueCents += payment.TotalAmount
			}
		}

		// Count disputes
		if payment.DisputeStatus != nil && *payment.DisputeStatus != "" {
			metrics.DisputeCount++
		}
	}
	metrics.TotalRevenue = float64(totalRevenueCents) / 100

	// Calculate refunded amount
	var refundedCents int64
	for _, refund := range refunds {
		if refund.Status == "succeeded" {
			refundedCents += refund.Amount
		}
	}
	metrics.RefundedAmount = float64(refundedCents) / 100

	// Churned MRR: sum of recurring amounts from subs canceled in last 30 days
	var churnedCents int64
	for _, sub := range subscriptions {
		if sub.Status == "cancelled" {
			updatedAt := parseTime(sub.UpdatedAt)
			if updatedAt.After(thirtyDaysAgo) {
				churnedCents += sub.RecurringAmount
			}
		}
	}

	metrics.ChurnedMRR = float64(churnedCents) / 100

	// Churn rate
	totalSubs := metrics.ActiveSubscriptions + metrics.CanceledSubscriptions
	if totalSubs > 0 {
		metrics.ChurnRate = float64(metrics.CanceledSubscriptions) / float64(totalSubs) * 100
	}

	// ARPU
	if metrics.ActiveCustomers > 0 {
		metrics.ARPU = metrics.MRR / float64(metrics.ActiveCustomers)
	}

	return metrics
}

// ---- Handlers ----

func (s *DodoService) GetRevenueMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache
	cacheKey := projectID + ":dodo_metrics"
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("Dodo metrics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	apiKey, err := s.getDodoAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  err.Error(),
			"data": map[string]interface{}{
				"configured": err.Error() != "dodo payments API key not configured",
			},
		})
		return
	}

	log.Printf("Fetching live Dodo metrics for project %s", projectID)

	// Fetch all data in parallel
	var payments []DodoPayment
	var subscriptions []DodoSubscription
	var customers []DodoCustomer
	var refunds []DodoRefund
	var paymentErr, subErr, custErr, refundErr error

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		payments, paymentErr = s.fetchPayments(apiKey)
	}()
	go func() {
		defer wg.Done()
		subscriptions, subErr = s.fetchSubscriptions(apiKey)
	}()
	go func() {
		defer wg.Done()
		customers, custErr = s.fetchCustomers(apiKey)
	}()
	go func() {
		defer wg.Done()
		refunds, refundErr = s.fetchRefunds(apiKey)
	}()

	wg.Wait()

	if paymentErr != nil {
		log.Printf("Warning: Failed to fetch payments: %v", paymentErr)
	}
	if subErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch Dodo data: %v", subErr),
		})
		return
	}
	if custErr != nil {
		log.Printf("Warning: Failed to fetch customers: %v", custErr)
	}
	if refundErr != nil {
		log.Printf("Warning: Failed to fetch refunds: %v", refundErr)
	}

	metrics := s.calculateMetrics(payments, subscriptions, customers, refunds)

	// Build time series
	timeSeries := s.buildTimeSeries(payments, subscriptions, customers, 30)

	// Count new subscriptions in the period
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	newSubs := 0
	for _, sub := range subscriptions {
		createdAt := parseTime(sub.CreatedAt)
		if createdAt.After(thirtyDaysAgo) {
			newSubs++
		}
	}

	response := map[string]interface{}{
		"mrr":                    metrics.MRR,
		"arr":                    metrics.ARR,
		"total_revenue":          metrics.TotalRevenue,
		"active_subscriptions":   metrics.ActiveSubscriptions,
		"canceled_subscriptions": metrics.CanceledSubscriptions,
		"on_hold_subscriptions":  metrics.OnHoldSubscriptions,
		"total_customers":        metrics.TotalCustomers,
		"active_customers":       metrics.ActiveCustomers,
		"churn_rate":             metrics.ChurnRate,
		"arpu":                   metrics.ARPU,
		"refunded_amount":        metrics.RefundedAmount,
		"dispute_count":          metrics.DisputeCount,
		"last_updated":           metrics.LastUpdated,
		"time_series":            timeSeries,
		"date_range": map[string]string{
			"start": time.Now().AddDate(0, 0, -30).Format("2006-01-02"),
			"end":   time.Now().Format("2006-01-02"),
		},
		"growth_rate":           0.0,
		"new_subscriptions":     newSubs,
		"churned_subscriptions": metrics.CanceledSubscriptions,
		"expansion_revenue":     0.0,
		"contraction_revenue":   0.0,
		"net_revenue":           metrics.TotalRevenue - metrics.RefundedAmount,
		"expansion_mrr":         0.0,
		"downgrade_mrr":         0.0,
		"churned_mrr":           metrics.ChurnedMRR,
		"net_revenue_churn":     0.0,
		// Stripe-compatible fields for frontend reuse
		"past_due_subscriptions":       metrics.OnHoldSubscriptions,
		"trialing_subscriptions":       0,
		"trial_to_pay_conversion_rate": 0.0,
	}

	s.cache.set(cacheKey, response, DodoCacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

func (s *DodoService) buildTimeSeries(payments []DodoPayment, subscriptions []DodoSubscription, customers []DodoCustomer, days int) []map[string]interface{} {
	startDate := time.Now().AddDate(0, 0, -days)

	// Daily revenue from payments
	dailyRevenue := make(map[string]int64)
	for _, payment := range payments {
		if payment.Status == "succeeded" {
			createdAt := parseTime(payment.CreatedAt)
			day := createdAt.Format("2006-01-02")
			dailyRevenue[day] += payment.TotalAmount
		}
	}

	// Track subscription lifecycle
	type subEvent struct {
		createdDay  string
		canceledDay string
		monthlyAmt  int64
		isActive    bool
		isCanceled  bool
	}
	var subs []subEvent
	for _, sub := range subscriptions {
		ev := subEvent{
			createdDay: parseTime(sub.CreatedAt).Format("2006-01-02"),
			monthlyAmt: sub.RecurringAmount,
			isActive:   sub.Status == "active",
			isCanceled: sub.Status == "cancelled",
		}
		if sub.Status == "cancelled" {
			ev.canceledDay = parseTime(sub.UpdatedAt).Format("2006-01-02")
		}
		subs = append(subs, ev)
	}

	// Daily churned MRR from canceled subscriptions
	dailyChurnedMRR := make(map[string]int64)
	for _, sub := range subs {
		if sub.isCanceled && sub.canceledDay != "" {
			dailyChurnedMRR[sub.canceledDay] += sub.monthlyAmt
		}
	}

	// Customer count by creation date
	dailyNewCustomers := make(map[string]int)
	customersBeforeWindow := 0
	windowStart := startDate.Format("2006-01-02")

	for _, cust := range customers {
		day := parseTime(cust.CreatedAt).Format("2006-01-02")
		dailyNewCustomers[day]++
		if day < windowStart {
			customersBeforeWindow++
		}
	}

	cumulativeCustomers := customersBeforeWindow

	var timeSeries []map[string]interface{}
	for i := 0; i <= days; i++ {
		day := startDate.AddDate(0, 0, i).Format("2006-01-02")

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
			"churn_rate":           math.Round(churnRate*100) / 100,
			"arpu":                 arpu,
			"new_customers":        dailyNewCustomers[day],
			"total_customers":      cumulativeCustomers,
			"expansion_mrr":        0.0,
			"downgrade_mrr":        0.0,
			"churned_mrr":          float64(dailyChurnedMRR[day]) / 100,
			"net_revenue_churn":    0.0,
		})
	}

	return timeSeries
}

func (s *DodoService) GetRevenueAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	days := 30

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	cacheKey := fmt.Sprintf("%s:dodo_analytics:%d", projectID, days)
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("Dodo analytics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	apiKey, err := s.getDodoAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("Fetching live Dodo analytics for project %s", projectID)

	// Fetch data in parallel
	var payments []DodoPayment
	var subscriptions []DodoSubscription
	var customers []DodoCustomer
	var refunds []DodoRefund
	var paymentErr, subErr, custErr, refundErr error

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); payments, paymentErr = s.fetchPayments(apiKey) }()
	go func() { defer wg.Done(); subscriptions, subErr = s.fetchSubscriptions(apiKey) }()
	go func() { defer wg.Done(); customers, custErr = s.fetchCustomers(apiKey) }()
	go func() { defer wg.Done(); refunds, refundErr = s.fetchRefunds(apiKey) }()
	wg.Wait()

	if subErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch analytics: %v", subErr),
		})
		return
	}
	if paymentErr != nil {
		log.Printf("Warning: Failed to fetch payments: %v", paymentErr)
	}
	if custErr != nil {
		log.Printf("Warning: Failed to fetch customers: %v", custErr)
	}
	if refundErr != nil {
		log.Printf("Warning: Failed to fetch refunds: %v", refundErr)
	}

	timeSeries := s.buildTimeSeries(payments, subscriptions, customers, days)
	metrics := s.calculateMetrics(payments, subscriptions, customers, refunds)

	summary := map[string]interface{}{}
	if metrics != nil {
		summary = map[string]interface{}{
			"current_mrr":          metrics.MRR,
			"current_arr":         metrics.ARR,
			"active_subscriptions": metrics.ActiveSubscriptions,
			"churn_rate":           metrics.ChurnRate,
			"arpu":                 metrics.ARPU,
			"trial_conversion_rate": 0.0,
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

	s.cache.set(cacheKey, response, DodoCacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

// DodoCustomerInfo mirrors StripeCustomerInfo for frontend compatibility
type DodoCustomerInfo struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	Created       time.Time `json:"created"`
	MRR           float64   `json:"mrr"`
	Status        string    `json:"status"`
	Subscriptions int       `json:"subscriptions"`
}

func (s *DodoService) GetCustomerAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	cacheKey := projectID + ":dodo_customer_analytics"
	if cached, ok := s.cache.get(cacheKey); ok {
		log.Printf("Dodo customer analytics cache hit for project %s", projectID)
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   cached,
			"cached": true,
		})
		return
	}

	apiKey, err := s.getDodoAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		return
	}

	log.Printf("Fetching live Dodo customer analytics for project %s", projectID)

	// Fetch customers and subscriptions in parallel
	var dodoCustomers []DodoCustomer
	var subscriptions []DodoSubscription
	var custErr, subErr error

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); dodoCustomers, custErr = s.fetchCustomers(apiKey) }()
	go func() { defer wg.Done(); subscriptions, subErr = s.fetchSubscriptions(apiKey) }()
	wg.Wait()

	if custErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "error",
			"error":  fmt.Sprintf("Failed to fetch customers: %v", custErr),
		})
		return
	}
	if subErr != nil {
		log.Printf("Warning: Failed to fetch subscriptions: %v", subErr)
	}

	// Build customer MRR and subscription count maps
	customerMRR := make(map[string]float64)
	customerSubCount := make(map[string]int)
	customerStatus := make(map[string]string)

	for _, sub := range subscriptions {
		if sub.Status == "active" {
			customerStatus[sub.CustomerID] = "active"
			customerSubCount[sub.CustomerID]++
			customerMRR[sub.CustomerID] += float64(sub.RecurringAmount) / 100
		} else if customerStatus[sub.CustomerID] == "" {
			customerStatus[sub.CustomerID] = sub.Status
		}
	}

	var customerInfos []DodoCustomerInfo
	totalMRR := 0.0
	paidCustomers := 0

	for _, cust := range dodoCustomers {
		status := customerStatus[cust.CustomerID]
		if status == "" {
			status = "free"
		}

		mrr := customerMRR[cust.CustomerID]
		if mrr > 0 {
			paidCustomers++
			totalMRR += mrr
		}

		customerInfos = append(customerInfos, DodoCustomerInfo{
			ID:            cust.CustomerID,
			Email:         cust.Email,
			Name:          cust.Name,
			Created:       parseTime(cust.CreatedAt),
			MRR:           mrr,
			Status:        status,
			Subscriptions: customerSubCount[cust.CustomerID],
		})
	}

	// Sort by MRR descending
	sort.Slice(customerInfos, func(i, j int) bool {
		return customerInfos[i].MRR > customerInfos[j].MRR
	})

	totalCustomers := len(customerInfos)
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
		"customers": customerInfos,
	}

	s.cache.set(cacheKey, response, DodoCustomersTTL)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   response,
		"cached": false,
	})
}

func (s *DodoService) SyncDodoDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getDodoAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Test connection by fetching one customer
	_, err = s.dodoRequest("GET", "/customers?page_size=1", apiKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Failed to connect to DodoPayments. Please check your API key.",
		})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	c.JSON(http.StatusOK, gin.H{
		"message": "DodoPayments connection verified. Data is fetched live from DodoPayments API with caching.",
		"note":    "Manual sync is no longer needed. Metrics are calculated in real-time.",
	})
}

func (s *DodoService) RefreshCacheHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("SECURITY: Unauthorized cache refresh attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	log.Printf("Cleared Dodo cache for project %s", projectID)

	c.JSON(http.StatusOK, gin.H{
		"message": "DodoPayments cache cleared. Next request will fetch fresh data.",
	})
}
