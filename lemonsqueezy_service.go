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

// LemonSqueezyService handles all Lemon Squeezy-related operations with live data fetching
type LemonSqueezyService struct {
	db               *gorm.DB
	cache            *LSCache
	projectListCache *BoundedCache[[]Project]
	responseCache    *BoundedCache[*CachedResponse]
	httpClient       *http.Client
}

const (
	LSBaseURL = "https://api.lemonsqueezy.com/v1"

	LSCacheTTL        = 5 * time.Minute
	LSCustomersTTL    = 10 * time.Minute
)

// LSCache provides in-memory caching for Lemon Squeezy data
type LSCache struct {
	mu      sync.RWMutex
	entries map[string]*LSCacheEntry
}

type LSCacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

func (c *LSCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Data, true
}

func (c *LSCache) set(key string, data interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &LSCacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (c *LSCache) invalidateProject(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if len(key) > len(projectID) && key[:len(projectID)] == projectID {
			delete(c.entries, key)
		}
	}
}

func NewLemonSqueezyService(db *gorm.DB) *LemonSqueezyService {
	return &LemonSqueezyService{
		db: db,
		cache: &LSCache{
			entries: make(map[string]*LSCacheEntry),
		},
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *LemonSqueezyService) invalidateAllCaches(projectID, accountID string) {
	s.cache.invalidateProject(projectID)

	if s.responseCache != nil {
		prefix := fmt.Sprintf("resp:/api/v1/projects/%s/lemonsqueezy/", projectID)
		s.responseCache.DeleteByPrefix(prefix)
	}

	if s.projectListCache != nil {
		cacheKey := fmt.Sprintf("account_projects:%s", accountID)
		s.projectListCache.Delete(cacheKey)
	}
}

func (s *LemonSqueezyService) getLSAPIKey(projectID, accountID string) (string, error) {
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		return "", fmt.Errorf("project not found or access denied")
	}

	if project.LemonSqueezyAPIKey == "" {
		return "", fmt.Errorf("lemon squeezy API key not configured")
	}

	return project.LemonSqueezyAPIKey, nil
}

// lsRequest makes an authenticated request to the Lemon Squeezy API (JSON:API format)
func (s *LemonSqueezyService) lsRequest(method, path, apiKey string) ([]byte, error) {
	url := LSBaseURL + path

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("Content-Type", "application/vnd.api+json")

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

// --- Lemon Squeezy JSON:API response types ---

type LSJSONAPIResponse struct {
	Data  json.RawMessage `json:"data"`
	Links *LSLinks        `json:"links"`
	Meta  *LSMeta         `json:"meta"`
}

type LSLinks struct {
	First string `json:"first"`
	Last  string `json:"last"`
	Next  string `json:"next"`
}

type LSMeta struct {
	Page LSPageMeta `json:"page"`
}

type LSPageMeta struct {
	CurrentPage int `json:"currentPage"`
	From        int `json:"from"`
	LastPage    int `json:"lastPage"`
	PerPage     int `json:"perPage"`
	To          int `json:"to"`
	Total       int `json:"total"`
}

type LSResource struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Attributes json.RawMessage `json:"attributes"`
}

// Parsed attribute types

type LSOrderAttrs struct {
	StoreID            int    `json:"store_id"`
	CustomerID         int    `json:"customer_id"`
	Status             string `json:"status"` // paid, pending, refunded, partial_refund, chargedback
	Currency           string `json:"currency"`
	Total              int    `json:"total"`              // in cents
	SubtotalFormatted  string `json:"subtotal_formatted"`
	TotalFormatted     string `json:"total_formatted"`
	RefundedAmount     int    `json:"refunded_amount"`
	FirstOrderItem     *LSOrderItemSummary `json:"first_order_item"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type LSOrderItemSummary struct {
	ID        int    `json:"id"`
	OrderID   int    `json:"order_id"`
	ProductID int    `json:"product_id"`
	VariantID int    `json:"variant_id"`
	Price     int    `json:"price"`
}

type LSSubscriptionAttrs struct {
	StoreID          int     `json:"store_id"`
	CustomerID       int     `json:"customer_id"`
	OrderID          int     `json:"order_id"`
	ProductID        int     `json:"product_id"`
	VariantID        int     `json:"variant_id"`
	Status           string  `json:"status"` // on_trial, active, paused, past_due, unpaid, cancelled, expired
	Price            int     `json:"price"`  // in cents
	RenewalPrice     int     `json:"renewal_price"`
	BillingAnchor    int     `json:"billing_anchor"`
	CardBrand        string  `json:"card_brand"`
	TrialEndsAt      *string `json:"trial_ends_at"`
	RenewsAt         *string `json:"renews_at"`
	EndsAt           *string `json:"ends_at"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type LSCustomerAttrs struct {
	StoreID   int    `json:"store_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Status    string `json:"status"`
	City      string `json:"city"`
	Region    string `json:"region"`
	Country   string `json:"country"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Internal parsed types for computation
type LSOrder struct {
	ID             string
	CustomerID     int
	Status         string
	Total          int
	RefundedAmount int
	Currency       string
	CreatedAt      string
}

type LSSubscription struct {
	ID           string
	CustomerID   int
	ProductID    int
	Status       string
	Price        int
	RenewalPrice int
	TrialEndsAt  *string
	RenewsAt     *string
	EndsAt       *string
	CreatedAt    string
}

type LSCustomer struct {
	ID        string
	Name      string
	Email     string
	CreatedAt string
}

// ---- Data Fetching ----

func (s *LemonSqueezyService) fetchAllLSData(apiKey string) ([]LSOrder, []LSSubscription, []LSCustomer, error) {
	var (
		orders        []LSOrder
		subscriptions []LSSubscription
		customers     []LSCustomer
		wg            sync.WaitGroup
		mu            sync.Mutex
		fetchErrors   []error
	)

	wg.Add(3)

	go func() {
		defer wg.Done()
		fetched, err := s.fetchAllOrders(apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("orders: %v", err))
		} else {
			orders = fetched
		}
	}()

	go func() {
		defer wg.Done()
		fetched, err := s.fetchAllSubscriptions(apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("subscriptions: %v", err))
		} else {
			subscriptions = fetched
		}
	}()

	go func() {
		defer wg.Done()
		fetched, err := s.fetchAllCustomers(apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("customers: %v", err))
		} else {
			customers = fetched
		}
	}()

	wg.Wait()

	if len(fetchErrors) > 0 {
		return orders, subscriptions, customers, fmt.Errorf("fetch errors: %v", fetchErrors)
	}

	return orders, subscriptions, customers, nil
}

func (s *LemonSqueezyService) fetchPaginated(path, apiKey string) ([]LSResource, error) {
	var allResources []LSResource
	currentPath := path

	for currentPath != "" {
		var body []byte
		var err error

		if currentPath == path {
			body, err = s.lsRequest("GET", currentPath, apiKey)
		} else {
			// For subsequent pages, the URL is already fully qualified
			req, reqErr := http.NewRequest("GET", currentPath, nil)
			if reqErr != nil {
				return allResources, reqErr
			}
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Accept", "application/vnd.api+json")

			resp, respErr := s.httpClient.Do(req)
			if respErr != nil {
				return allResources, respErr
			}
			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
			if err != nil {
				return allResources, err
			}
			if resp.StatusCode != http.StatusOK {
				return allResources, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
			}
		}

		if err != nil {
			return allResources, err
		}

		var resp LSJSONAPIResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allResources, fmt.Errorf("failed to parse response: %v", err)
		}

		var resources []LSResource
		if err := json.Unmarshal(resp.Data, &resources); err != nil {
			return allResources, fmt.Errorf("failed to parse data: %v", err)
		}

		allResources = append(allResources, resources...)

		if resp.Links != nil && resp.Links.Next != "" {
			currentPath = resp.Links.Next
		} else {
			break
		}
	}

	return allResources, nil
}

func (s *LemonSqueezyService) fetchAllOrders(apiKey string) ([]LSOrder, error) {
	resources, err := s.fetchPaginated("/orders?page[size]=100", apiKey)
	if err != nil {
		return nil, err
	}

	var orders []LSOrder
	for _, r := range resources {
		var attrs LSOrderAttrs
		if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
			continue
		}
		orders = append(orders, LSOrder{
			ID:             r.ID,
			CustomerID:     attrs.CustomerID,
			Status:         attrs.Status,
			Total:          attrs.Total,
			RefundedAmount: attrs.RefundedAmount,
			Currency:       attrs.Currency,
			CreatedAt:      attrs.CreatedAt,
		})
	}
	return orders, nil
}

func (s *LemonSqueezyService) fetchAllSubscriptions(apiKey string) ([]LSSubscription, error) {
	resources, err := s.fetchPaginated("/subscriptions?page[size]=100", apiKey)
	if err != nil {
		return nil, err
	}

	var subs []LSSubscription
	for _, r := range resources {
		var attrs LSSubscriptionAttrs
		if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
			continue
		}
		subs = append(subs, LSSubscription{
			ID:           r.ID,
			CustomerID:   attrs.CustomerID,
			ProductID:    attrs.ProductID,
			Status:       attrs.Status,
			Price:        attrs.Price,
			RenewalPrice: attrs.RenewalPrice,
			TrialEndsAt:  attrs.TrialEndsAt,
			RenewsAt:     attrs.RenewsAt,
			EndsAt:       attrs.EndsAt,
			CreatedAt:    attrs.CreatedAt,
		})
	}
	return subs, nil
}

func (s *LemonSqueezyService) fetchAllCustomers(apiKey string) ([]LSCustomer, error) {
	resources, err := s.fetchPaginated("/customers?page[size]=100", apiKey)
	if err != nil {
		return nil, err
	}

	var customers []LSCustomer
	for _, r := range resources {
		var attrs LSCustomerAttrs
		if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
			continue
		}
		customers = append(customers, LSCustomer{
			ID:        r.ID,
			Name:      attrs.Name,
			Email:     attrs.Email,
			CreatedAt: attrs.CreatedAt,
		})
	}
	return customers, nil
}

// ---- Metrics Computation ----

func (s *LemonSqueezyService) computeMetrics(orders []LSOrder, subscriptions []LSSubscription, customers []LSCustomer) *PolarMetrics {
	now := time.Now()
	metrics := &PolarMetrics{
		Date:        now.Format("2006-01-02"),
		LastUpdated: now.Format(time.RFC3339),
	}

	thirtyDaysAgo := now.AddDate(0, 0, -30)

	var activeSubs, canceledSubs, trialSubs, newSubsThisMonth int
	var totalMRR float64

	for _, sub := range subscriptions {
		switch sub.Status {
		case "active":
			activeSubs++
			totalMRR += float64(sub.RenewalPrice) / 100.0
		case "cancelled", "expired":
			canceledSubs++
		case "on_trial":
			trialSubs++
		}

		createdAt, err := time.Parse(time.RFC3339Nano, sub.CreatedAt)
		if err == nil && createdAt.After(thirtyDaysAgo) {
			newSubsThisMonth++
		}
	}

	metrics.MRR = totalMRR
	metrics.ARR = totalMRR * 12
	metrics.ActiveSubscriptions = activeSubs
	metrics.CanceledSubscriptions = canceledSubs
	metrics.NewSubscriptions = newSubsThisMonth

	var totalRevenue float64
	var totalRefunded float64
	for _, order := range orders {
		if order.Status == "paid" {
			totalRevenue += float64(order.Total) / 100.0
		}
		totalRefunded += float64(order.RefundedAmount) / 100.0
	}

	metrics.TotalRevenue = totalRevenue
	metrics.NetRevenue = totalRevenue - totalRefunded

	metrics.TotalCustomers = len(customers)

	activeCustomerIDs := make(map[int]bool)
	for _, sub := range subscriptions {
		if sub.Status == "active" {
			activeCustomerIDs[sub.CustomerID] = true
		}
	}
	metrics.ActiveCustomers = len(activeCustomerIDs)

	if metrics.ActiveCustomers > 0 {
		metrics.ARPU = totalMRR / float64(metrics.ActiveCustomers)
	}

	var churnedThisMonth int
	for _, sub := range subscriptions {
		if sub.EndsAt != nil {
			endedAt, err := time.Parse(time.RFC3339Nano, *sub.EndsAt)
			if err == nil && endedAt.After(thirtyDaysAgo) && (sub.Status == "cancelled" || sub.Status == "expired") {
				churnedThisMonth++
			}
		}
	}
	metrics.ChurnedSubscriptions = churnedThisMonth

	totalAtStart := activeSubs + churnedThisMonth
	if totalAtStart > 0 {
		metrics.ChurnRate = float64(churnedThisMonth) / float64(totalAtStart) * 100
	}

	if totalAtStart > 0 {
		metrics.GrowthRate = float64(newSubsThisMonth-churnedThisMonth) / float64(totalAtStart) * 100
	}

	if metrics.ChurnRate > 0 {
		metrics.CustomerLifetimeValue = metrics.ARPU / (metrics.ChurnRate / 100)
	} else if metrics.ARPU > 0 {
		metrics.CustomerLifetimeValue = metrics.ARPU * 24
	}

	paidFromTrial := 0
	for _, sub := range subscriptions {
		if sub.Status == "active" && sub.TrialEndsAt != nil {
			paidFromTrial++
		}
	}
	totalTrials := trialSubs + paidFromTrial
	if totalTrials > 0 {
		metrics.TrialToPayConversionRate = float64(paidFromTrial) / float64(totalTrials) * 100
	}

	return metrics
}

// ---- Time Series ----

func (s *LemonSqueezyService) buildTimeSeries(orders []LSOrder, subscriptions []LSSubscription, customers []LSCustomer, startDate, endDate time.Time) []PolarTimeSeriesPoint {
	var points []PolarTimeSeriesPoint

	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		point := PolarTimeSeriesPoint{Date: dateStr}

		for _, order := range orders {
			orderDate, err := time.Parse(time.RFC3339Nano, order.CreatedAt)
			if err != nil {
				continue
			}
			if orderDate.Format("2006-01-02") == dateStr && order.Status == "paid" {
				point.Revenue += float64(order.Total) / 100.0
			}
		}

		var activeSubs int
		var mrr float64
		for _, sub := range subscriptions {
			createdAt, err := time.Parse(time.RFC3339Nano, sub.CreatedAt)
			if err != nil || createdAt.After(d.Add(24*time.Hour)) {
				continue
			}

			isActive := sub.Status == "active" || sub.Status == "on_trial"
			if sub.EndsAt != nil {
				endedAt, err := time.Parse(time.RFC3339Nano, *sub.EndsAt)
				if err == nil && endedAt.Before(d) {
					isActive = false
				}
			}

			if isActive {
				activeSubs++
				mrr += float64(sub.RenewalPrice) / 100.0
			}
		}

		point.ActiveSubscriptions = activeSubs
		point.MRR = math.Round(mrr*100) / 100
		point.ARR = math.Round(mrr*12*100) / 100

		if activeSubs > 0 {
			point.ARPU = math.Round(mrr/float64(activeSubs)*100) / 100
		}

		for _, c := range customers {
			custDate, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
			if err != nil {
				continue
			}
			if custDate.Format("2006-01-02") == dateStr {
				point.NewCustomers++
			}
		}

		totalCust := 0
		for _, c := range customers {
			custDate, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
			if err != nil {
				continue
			}
			if !custDate.After(d.Add(24 * time.Hour)) {
				totalCust++
			}
		}
		point.TotalCustomers = totalCust

		points = append(points, point)
	}

	return points
}

// ---- API Key Management ----

type UpdateLSAPIKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

func (s *LemonSqueezyService) UpdateLSAPIKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	var req UpdateLSAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.ApiKey) < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key format"})
		return
	}

	// Test the API key
	testReq, err := http.NewRequest("GET", LSBaseURL+"/customers?page[size]=1", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate API key"})
		return
	}
	testReq.Header.Set("Authorization", "Bearer "+req.ApiKey)
	testReq.Header.Set("Accept", "application/vnd.api+json")

	resp, err := s.httpClient.Do(testReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to connect to Lemon Squeezy. Please check your API key."})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Lemon Squeezy API key"})
		return
	}

	if err := s.db.Model(&Project{}).Where("id = ? AND account_id = ?", projectID, accountID.(string)).Update("lemonsqueezy_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Lemon Squeezy API key"})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Lemon Squeezy API key updated successfully",
	})
}

// ---- Sync Handler ----

func (s *LemonSqueezyService) SyncLSDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getLSAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	_, err = s.lsRequest("GET", "/customers?page[size]=1", apiKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to connect to Lemon Squeezy API: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Lemon Squeezy data cache cleared. Fresh data will be fetched on next request.",
	})
}

// ---- Revenue Metrics Handler ----

func (s *LemonSqueezyService) GetRevenueMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getLSAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cacheKey := projectID + ":ls:metrics"
	if cached, ok := s.cache.get(cacheKey); ok {
		if metrics, ok := cached.(*PolarMetrics); ok {
			c.JSON(http.StatusOK, gin.H{"status": "success", "data": metrics})
			return
		}
	}

	orders, subscriptions, customers, err := s.fetchAllLSData(apiKey)
	if err != nil {
		log.Printf("Warning: Lemon Squeezy data fetch had errors: %v", err)
	}

	metrics := s.computeMetrics(orders, subscriptions, customers)

	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -30)
	timeSeries := s.buildTimeSeries(orders, subscriptions, customers, startDate, endDate)

	response := gin.H{
		"date":                         metrics.Date,
		"mrr":                          metrics.MRR,
		"arr":                          metrics.ARR,
		"total_revenue":                metrics.TotalRevenue,
		"active_subscriptions":         metrics.ActiveSubscriptions,
		"canceled_subscriptions":       metrics.CanceledSubscriptions,
		"new_subscriptions":            metrics.NewSubscriptions,
		"churned_subscriptions":        metrics.ChurnedSubscriptions,
		"expansion_revenue":            metrics.ExpansionRevenue,
		"contraction_revenue":          metrics.ContractionRevenue,
		"net_revenue":                  metrics.NetRevenue,
		"expansion_mrr":                metrics.ExpansionMRR,
		"downgrade_mrr":                metrics.DowngradeMRR,
		"churned_mrr":                  metrics.ChurnedMRR,
		"net_revenue_churn":            metrics.NetRevenueChurn,
		"churn_rate":                   metrics.ChurnRate,
		"growth_rate":                  metrics.GrowthRate,
		"arpu":                         metrics.ARPU,
		"customer_lifetime_value":      metrics.CustomerLifetimeValue,
		"trial_to_pay_conversion_rate": metrics.TrialToPayConversionRate,
		"total_customers":              metrics.TotalCustomers,
		"active_customers":             metrics.ActiveCustomers,
		"last_updated":                 metrics.LastUpdated,
		"time_series":                  timeSeries,
		"date_range": gin.H{
			"start": startDate.Format("2006-01-02"),
			"end":   endDate.Format("2006-01-02"),
		},
	}

	s.cache.set(cacheKey, metrics, LSCacheTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}

// ---- Revenue Analytics Handler ----

func (s *LemonSqueezyService) GetRevenueAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getLSAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -30)

	if startDateStr != "" {
		if parsed, err := time.Parse("2006-01-02", startDateStr); err == nil {
			startDate = parsed
		}
	}
	if endDateStr != "" {
		if parsed, err := time.Parse("2006-01-02", endDateStr); err == nil {
			endDate = parsed
		}
	}

	cacheKey := fmt.Sprintf("%s:ls:analytics:%s:%s", projectID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if cached, ok := s.cache.get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": cached})
		return
	}

	orders, subscriptions, customers, err := s.fetchAllLSData(apiKey)
	if err != nil {
		log.Printf("Warning: Lemon Squeezy data fetch had errors: %v", err)
	}

	timeSeries := s.buildTimeSeries(orders, subscriptions, customers, startDate, endDate)
	metrics := s.computeMetrics(orders, subscriptions, customers)

	response := gin.H{
		"summary": gin.H{
			"current_mrr":           metrics.MRR,
			"current_arr":           metrics.ARR,
			"active_subscriptions":  metrics.ActiveSubscriptions,
			"churn_rate":            metrics.ChurnRate,
			"arpu":                  metrics.ARPU,
			"trial_conversion_rate": metrics.TrialToPayConversionRate,
		},
		"time_series": timeSeries,
		"date_range": gin.H{
			"start": startDate.Format("2006-01-02"),
			"end":   endDate.Format("2006-01-02"),
		},
	}

	s.cache.set(cacheKey, response, LSCacheTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}

// ---- Customer Analytics Handler ----

func (s *LemonSqueezyService) GetCustomerAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getLSAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cacheKey := projectID + ":ls:customers"
	if cached, ok := s.cache.get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": cached})
		return
	}

	_, subscriptions, customers, err := s.fetchAllLSData(apiKey)
	if err != nil {
		log.Printf("Warning: Lemon Squeezy data fetch had errors: %v", err)
	}

	customerMRR := make(map[int]float64)
	customerSubCount := make(map[int]int)
	for _, sub := range subscriptions {
		if sub.Status == "active" {
			customerMRR[sub.CustomerID] += float64(sub.RenewalPrice) / 100.0
			customerSubCount[sub.CustomerID]++
		}
	}

	// Build customer ID to parsed customer map
	customerIDMap := make(map[string]LSCustomer)
	for _, c := range customers {
		customerIDMap[c.ID] = c
	}

	var customerInfos []PolarCustomerInfo
	var paidCount, freeCount int
	var totalMRR float64

	for _, c := range customers {
		// Try to find MRR (LS uses int customer IDs internally)
		var mrr float64
		for custID, mrrVal := range customerMRR {
			if fmt.Sprintf("%d", custID) == c.ID {
				mrr = mrrVal
				break
			}
		}

		status := "free"
		if mrr > 0 {
			status = "active"
			paidCount++
		} else {
			freeCount++
		}
		totalMRR += mrr

		subCount := 0
		for custID, count := range customerSubCount {
			if fmt.Sprintf("%d", custID) == c.ID {
				subCount = count
				break
			}
		}

		customerInfos = append(customerInfos, PolarCustomerInfo{
			ID:            c.ID,
			Email:         c.Email,
			Name:          c.Name,
			MRR:           math.Round(mrr*100) / 100,
			Status:        status,
			Created:       c.CreatedAt,
			Subscriptions: subCount,
		})
	}

	sort.Slice(customerInfos, func(i, j int) bool {
		return customerInfos[i].MRR > customerInfos[j].MRR
	})

	avgMRR := 0.0
	if len(customerInfos) > 0 {
		avgMRR = totalMRR / float64(len(customerInfos))
	}

	conversionRate := 0.0
	if len(customerInfos) > 0 {
		conversionRate = float64(paidCount) / float64(len(customerInfos)) * 100
	}

	response := gin.H{
		"summary": gin.H{
			"total_customers": len(customerInfos),
			"paid_customers":  paidCount,
			"free_customers":  freeCount,
			"total_mrr":       math.Round(totalMRR*100) / 100,
			"avg_mrr":         math.Round(avgMRR*100) / 100,
			"conversion_rate": math.Round(conversionRate*100) / 100,
		},
		"customers": customerInfos,
	}

	s.cache.set(cacheKey, response, LSCustomersTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}
