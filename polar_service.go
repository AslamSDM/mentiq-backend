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

// PolarService handles all Polar-related operations with live data fetching
type PolarService struct {
	db               *gorm.DB
	cache            *PolarCache
	projectListCache *BoundedCache[[]Project]
	responseCache    *BoundedCache[*CachedResponse]
	httpClient       *http.Client
}

const (
	PolarBaseURL = "https://api.polar.sh"

	PolarCacheTTL        = 5 * time.Minute
	PolarCustomersTTL    = 10 * time.Minute
	PolarSubscriptionTTL = 5 * time.Minute
)

// PolarCache provides in-memory caching for Polar data
type PolarCache struct {
	mu      sync.RWMutex
	entries map[string]*PolarCacheEntry
}

type PolarCacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

func (c *PolarCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists || time.Now().After(entry.ExpiresAt) {
		return nil, false
	}
	return entry.Data, true
}

func (c *PolarCache) set(key string, data interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &PolarCacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

func (c *PolarCache) invalidateProject(projectID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if len(key) > len(projectID) && key[:len(projectID)] == projectID {
			delete(c.entries, key)
		}
	}
}

// NewPolarService creates a new PolarService instance
func NewPolarService(db *gorm.DB) *PolarService {
	return &PolarService{
		db: db,
		cache: &PolarCache{
			entries: make(map[string]*PolarCacheEntry),
		},
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *PolarService) invalidateAllCaches(projectID, accountID string) {
	s.cache.invalidateProject(projectID)

	if s.responseCache != nil {
		prefix := fmt.Sprintf("resp:/api/v1/projects/%s/polar/", projectID)
		s.responseCache.DeleteByPrefix(prefix)
	}

	if s.projectListCache != nil {
		cacheKey := fmt.Sprintf("account_projects:%s", accountID)
		s.projectListCache.Delete(cacheKey)
	}
}

// getPolarAPIKey retrieves and validates the Polar API key for a project
func (s *PolarService) getPolarAPIKey(projectID, accountID string) (string, error) {
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		log.Printf("SECURITY: Polar access attempt - Account: %s, Project: %s - project not found or access denied", accountID, projectID)
		return "", fmt.Errorf("project not found or access denied")
	}

	if project.PolarAPIKey == "" {
		return "", fmt.Errorf("polar API key not configured")
	}

	return project.PolarAPIKey, nil
}

// polarRequest makes an authenticated request to the Polar API
func (s *PolarService) polarRequest(method, path, apiKey string) ([]byte, error) {
	url := PolarBaseURL + path

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

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

// --- Polar API response types ---

// PolarOrder represents an order from Polar API
type PolarOrder struct {
	ID             string          `json:"id"`
	Amount         int64           `json:"amount"`
	TaxAmount      int64           `json:"tax_amount"`
	Currency       string          `json:"currency"`
	Status         string          `json:"status"` // pending, paid, refunded, etc.
	BillingReason  string          `json:"billing_reason"` // purchase, subscription_create, subscription_cycle, subscription_update
	CreatedAt      string          `json:"created_at"`
	ModifiedAt     string          `json:"modified_at"`
	CustomerID     string          `json:"customer_id"`
	ProductID      string          `json:"product_id"`
	SubscriptionID *string         `json:"subscription_id"`
	Customer       PolarCustomer   `json:"customer"`
	Product        PolarProduct    `json:"product"`
	Subscription   *json.RawMessage `json:"subscription"`
}

// PolarSubscription represents a subscription from Polar API
type PolarSubscription struct {
	ID                 string       `json:"id"`
	Status             string       `json:"status"` // active, canceled, incomplete, incomplete_expired, past_due, trialing, unpaid
	CurrentPeriodStart string       `json:"current_period_start"`
	CurrentPeriodEnd   *string      `json:"current_period_end"`
	CancelAtPeriodEnd  bool         `json:"cancel_at_period_end"`
	CanceledAt         *string      `json:"canceled_at"`
	StartedAt          *string      `json:"started_at"`
	EndsAt             *string      `json:"ends_at"`
	EndedAt            *string      `json:"ended_at"`
	Amount             int64        `json:"amount"`
	Currency           string       `json:"currency"`
	RecurringInterval  string       `json:"recurring_interval"` // month, year
	CustomerID         string       `json:"customer_id"`
	ProductID          string       `json:"product_id"`
	CreatedAt          string       `json:"created_at"`
	ModifiedAt         string       `json:"modified_at"`
	Customer           PolarCustomer `json:"customer"`
	Product            PolarProduct  `json:"product"`
}

// PolarCustomer represents a customer from Polar API
type PolarCustomer struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// PolarProduct represents a product from Polar API
type PolarProduct struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// PolarRefund represents a refund from Polar API
type PolarRefund struct {
	ID        string `json:"id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Reason    string `json:"reason"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	OrderID   string `json:"order_id"`
}

// PolarListResponse represents a paginated list response from Polar API
type PolarListResponse struct {
	Items      json.RawMessage `json:"items"`
	Pagination PolarPagination `json:"pagination"`
}

type PolarPagination struct {
	TotalCount  int `json:"total_count"`
	MaxPage     int `json:"max_page"`
}

// PolarMetrics is the internal computed metrics
type PolarMetrics struct {
	Date                     string  `json:"date"`
	MRR                      float64 `json:"mrr"`
	ARR                      float64 `json:"arr"`
	TotalRevenue             float64 `json:"total_revenue"`
	ActiveSubscriptions      int     `json:"active_subscriptions"`
	CanceledSubscriptions    int     `json:"canceled_subscriptions"`
	NewSubscriptions         int     `json:"new_subscriptions"`
	ChurnedSubscriptions     int     `json:"churned_subscriptions"`
	ExpansionRevenue         float64 `json:"expansion_revenue"`
	ContractionRevenue       float64 `json:"contraction_revenue"`
	NetRevenue               float64 `json:"net_revenue"`
	ExpansionMRR             float64 `json:"expansion_mrr"`
	DowngradeMRR             float64 `json:"downgrade_mrr"`
	ChurnedMRR               float64 `json:"churned_mrr"`
	NetRevenueChurn          float64 `json:"net_revenue_churn"`
	ChurnRate                float64 `json:"churn_rate"`
	GrowthRate               float64 `json:"growth_rate"`
	ARPU                     float64 `json:"arpu"`
	CustomerLifetimeValue    float64 `json:"customer_lifetime_value"`
	TrialToPayConversionRate float64 `json:"trial_to_pay_conversion_rate"`
	TotalCustomers           int     `json:"total_customers"`
	ActiveCustomers          int     `json:"active_customers"`
	LastUpdated              string  `json:"last_updated"`
}

type PolarCustomerInfo struct {
	ID            string  `json:"id"`
	Email         string  `json:"email"`
	Name          string  `json:"name"`
	MRR           float64 `json:"mrr"`
	Status        string  `json:"status"`
	Created       string  `json:"created"`
	Subscriptions int     `json:"subscriptions"`
}

// ---- Data Fetching ----

func (s *PolarService) fetchAllPolarData(apiKey string) ([]PolarOrder, []PolarSubscription, []PolarCustomer, []PolarRefund, error) {
	var (
		orders        []PolarOrder
		subscriptions []PolarSubscription
		customers     []PolarCustomer
		refunds       []PolarRefund
		wg            sync.WaitGroup
		mu            sync.Mutex
		fetchErrors   []error
	)

	wg.Add(4)

	// Fetch orders
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

	// Fetch subscriptions
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

	// Fetch customers
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

	// Fetch refunds
	go func() {
		defer wg.Done()
		fetched, err := s.fetchAllRefunds(apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("refunds: %v", err))
		} else {
			refunds = fetched
		}
	}()

	wg.Wait()

	if len(fetchErrors) > 0 {
		return orders, subscriptions, customers, refunds, fmt.Errorf("fetch errors: %v", fetchErrors)
	}

	return orders, subscriptions, customers, refunds, nil
}

func (s *PolarService) fetchAllOrders(apiKey string) ([]PolarOrder, error) {
	var allOrders []PolarOrder
	page := 1
	for {
		body, err := s.polarRequest("GET", fmt.Sprintf("/v1/orders/?page=%d&limit=100", page), apiKey)
		if err != nil {
			return allOrders, err
		}

		var resp PolarListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allOrders, fmt.Errorf("failed to parse orders response: %v", err)
		}

		var orders []PolarOrder
		if err := json.Unmarshal(resp.Items, &orders); err != nil {
			return allOrders, fmt.Errorf("failed to parse orders items: %v", err)
		}

		allOrders = append(allOrders, orders...)

		if page >= resp.Pagination.MaxPage || len(orders) == 0 {
			break
		}
		page++
	}
	return allOrders, nil
}

func (s *PolarService) fetchAllSubscriptions(apiKey string) ([]PolarSubscription, error) {
	var allSubs []PolarSubscription
	page := 1
	for {
		body, err := s.polarRequest("GET", fmt.Sprintf("/v1/subscriptions/?page=%d&limit=100", page), apiKey)
		if err != nil {
			return allSubs, err
		}

		var resp PolarListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allSubs, fmt.Errorf("failed to parse subscriptions response: %v", err)
		}

		var subs []PolarSubscription
		if err := json.Unmarshal(resp.Items, &subs); err != nil {
			return allSubs, fmt.Errorf("failed to parse subscriptions items: %v", err)
		}

		allSubs = append(allSubs, subs...)

		if page >= resp.Pagination.MaxPage || len(subs) == 0 {
			break
		}
		page++
	}
	return allSubs, nil
}

func (s *PolarService) fetchAllCustomers(apiKey string) ([]PolarCustomer, error) {
	var allCustomers []PolarCustomer
	page := 1
	for {
		body, err := s.polarRequest("GET", fmt.Sprintf("/v1/customers/?page=%d&limit=100", page), apiKey)
		if err != nil {
			return allCustomers, err
		}

		var resp PolarListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allCustomers, fmt.Errorf("failed to parse customers response: %v", err)
		}

		var customers []PolarCustomer
		if err := json.Unmarshal(resp.Items, &customers); err != nil {
			return allCustomers, fmt.Errorf("failed to parse customers items: %v", err)
		}

		allCustomers = append(allCustomers, customers...)

		if page >= resp.Pagination.MaxPage || len(customers) == 0 {
			break
		}
		page++
	}
	return allCustomers, nil
}

func (s *PolarService) fetchAllRefunds(apiKey string) ([]PolarRefund, error) {
	var allRefunds []PolarRefund
	page := 1
	for {
		body, err := s.polarRequest("GET", fmt.Sprintf("/v1/refunds/?page=%d&limit=100", page), apiKey)
		if err != nil {
			return allRefunds, err
		}

		var resp PolarListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return allRefunds, fmt.Errorf("failed to parse refunds response: %v", err)
		}

		var refunds []PolarRefund
		if err := json.Unmarshal(resp.Items, &refunds); err != nil {
			return allRefunds, fmt.Errorf("failed to parse refunds items: %v", err)
		}

		allRefunds = append(allRefunds, refunds...)

		if page >= resp.Pagination.MaxPage || len(refunds) == 0 {
			break
		}
		page++
	}
	return allRefunds, nil
}

// ---- Metrics Computation ----

func (s *PolarService) computeMetrics(orders []PolarOrder, subscriptions []PolarSubscription, customers []PolarCustomer, refunds []PolarRefund) *PolarMetrics {
	now := time.Now()
	metrics := &PolarMetrics{
		Date:        now.Format("2006-01-02"),
		LastUpdated: now.Format(time.RFC3339),
	}

	// Count subscription states
	var activeSubs, canceledSubs, trialSubs, newSubsThisMonth int
	var totalMRR float64

	thirtyDaysAgo := now.AddDate(0, 0, -30)

	for _, sub := range subscriptions {
		switch sub.Status {
		case "active":
			activeSubs++
			// Calculate MRR based on recurring interval
			amount := float64(sub.Amount) / 100.0
			if sub.RecurringInterval == "year" {
				totalMRR += amount / 12.0
			} else {
				totalMRR += amount
			}
		case "canceled":
			canceledSubs++
		case "trialing":
			trialSubs++
		}

		createdAt, err := time.Parse(time.RFC3339, sub.CreatedAt)
		if err == nil && createdAt.After(thirtyDaysAgo) {
			newSubsThisMonth++
		}
	}

	metrics.MRR = totalMRR
	metrics.ARR = totalMRR * 12
	metrics.ActiveSubscriptions = activeSubs
	metrics.CanceledSubscriptions = canceledSubs
	metrics.NewSubscriptions = newSubsThisMonth

	// Calculate total revenue from orders
	var totalRevenue float64
	var totalRefundAmount float64
	for _, order := range orders {
		if order.Status == "paid" {
			totalRevenue += float64(order.Amount) / 100.0
		}
	}
	for _, refund := range refunds {
		totalRefundAmount += float64(refund.Amount) / 100.0
	}

	metrics.TotalRevenue = totalRevenue
	metrics.NetRevenue = totalRevenue - totalRefundAmount

	// Customer counts
	metrics.TotalCustomers = len(customers)

	// Active customers = customers with active subscriptions
	activeCustomerIDs := make(map[string]bool)
	for _, sub := range subscriptions {
		if sub.Status == "active" {
			activeCustomerIDs[sub.CustomerID] = true
		}
	}
	metrics.ActiveCustomers = len(activeCustomerIDs)

	// ARPU
	if metrics.ActiveCustomers > 0 {
		metrics.ARPU = totalMRR / float64(metrics.ActiveCustomers)
	}

	// Churn rate (canceled in last 30 days / total at start)
	var churnedThisMonth int
	for _, sub := range subscriptions {
		if sub.CanceledAt != nil {
			canceledAt, err := time.Parse(time.RFC3339, *sub.CanceledAt)
			if err == nil && canceledAt.After(thirtyDaysAgo) {
				churnedThisMonth++
			}
		}
	}
	metrics.ChurnedSubscriptions = churnedThisMonth

	totalAtStart := activeSubs + churnedThisMonth
	if totalAtStart > 0 {
		metrics.ChurnRate = float64(churnedThisMonth) / float64(totalAtStart) * 100
	}

	// Growth rate
	if totalAtStart > 0 {
		metrics.GrowthRate = float64(newSubsThisMonth-churnedThisMonth) / float64(totalAtStart) * 100
	}

	// CLV estimate
	if metrics.ChurnRate > 0 {
		metrics.CustomerLifetimeValue = metrics.ARPU / (metrics.ChurnRate / 100)
	} else if metrics.ARPU > 0 {
		metrics.CustomerLifetimeValue = metrics.ARPU * 24 // Default to 24 months
	}

	// Trial conversion rate
	paidFromTrial := 0
	for _, sub := range subscriptions {
		if sub.Status == "active" {
			// Check if came from trial by looking at billing reason in orders
			for _, order := range orders {
				if order.SubscriptionID != nil && *order.SubscriptionID == sub.ID && order.BillingReason == "subscription_create" {
					paidFromTrial++
					break
				}
			}
		}
	}
	totalTrials := trialSubs + paidFromTrial
	if totalTrials > 0 {
		metrics.TrialToPayConversionRate = float64(paidFromTrial) / float64(totalTrials) * 100
	}

	return metrics
}

// ---- Time Series ----

type PolarTimeSeriesPoint struct {
	Date               string  `json:"date"`
	MRR                float64 `json:"mrr"`
	ARR                float64 `json:"arr"`
	Revenue            float64 `json:"revenue"`
	ActiveSubscriptions int    `json:"active_subscriptions"`
	ChurnRate          float64 `json:"churn_rate"`
	ARPU               float64 `json:"arpu"`
	NewCustomers       int     `json:"new_customers"`
	TotalCustomers     int     `json:"total_customers"`
	ExpansionMRR       float64 `json:"expansion_mrr"`
	DowngradeMRR       float64 `json:"downgrade_mrr"`
	ChurnedMRR         float64 `json:"churned_mrr"`
	NetRevenueChurn    float64 `json:"net_revenue_churn"`
}

func (s *PolarService) buildTimeSeries(orders []PolarOrder, subscriptions []PolarSubscription, customers []PolarCustomer, startDate, endDate time.Time) []PolarTimeSeriesPoint {
	var points []PolarTimeSeriesPoint

	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		point := PolarTimeSeriesPoint{Date: dateStr}

		// Revenue for this day
		for _, order := range orders {
			orderDate, err := time.Parse(time.RFC3339, order.CreatedAt)
			if err != nil {
				continue
			}
			if orderDate.Format("2006-01-02") == dateStr && order.Status == "paid" {
				point.Revenue += float64(order.Amount) / 100.0
			}
		}

		// Active subscriptions and MRR as of this day
		var activeSubs int
		var mrr float64
		for _, sub := range subscriptions {
			createdAt, err := time.Parse(time.RFC3339, sub.CreatedAt)
			if err != nil || createdAt.After(d.Add(24*time.Hour)) {
				continue
			}

			isActive := sub.Status == "active" || sub.Status == "trialing"
			if sub.CanceledAt != nil {
				canceledAt, err := time.Parse(time.RFC3339, *sub.CanceledAt)
				if err == nil && canceledAt.Before(d) {
					isActive = false
				}
			}
			if sub.EndedAt != nil {
				endedAt, err := time.Parse(time.RFC3339, *sub.EndedAt)
				if err == nil && endedAt.Before(d) {
					isActive = false
				}
			}

			if isActive {
				activeSubs++
				amount := float64(sub.Amount) / 100.0
				if sub.RecurringInterval == "year" {
					mrr += amount / 12.0
				} else {
					mrr += amount
				}
			}
		}

		point.ActiveSubscriptions = activeSubs
		point.MRR = math.Round(mrr*100) / 100
		point.ARR = math.Round(mrr*12*100) / 100

		// ARPU
		if activeSubs > 0 {
			point.ARPU = math.Round(mrr/float64(activeSubs)*100) / 100
		}

		// New customers on this day
		for _, c := range customers {
			custDate, err := time.Parse(time.RFC3339, c.CreatedAt)
			if err != nil {
				continue
			}
			if custDate.Format("2006-01-02") == dateStr {
				point.NewCustomers++
			}
		}

		// Total customers up to this day
		totalCust := 0
		for _, c := range customers {
			custDate, err := time.Parse(time.RFC3339, c.CreatedAt)
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

type UpdatePolarAPIKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

func (s *PolarService) UpdatePolarAPIKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("SECURITY: Unauthorized Polar API key update attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	var req UpdatePolarAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.ApiKey) < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key format"})
		return
	}

	// Test the API key by listing products (lightweight call)
	testReq, err := http.NewRequest("GET", PolarBaseURL+"/v1/products/?limit=1", nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate API key"})
		return
	}
	testReq.Header.Set("Authorization", "Bearer "+req.ApiKey)
	testReq.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(testReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to connect to Polar. Please check your API key."})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Polar API key or insufficient permissions"})
		return
	}

	if err := s.db.Model(&Project{}).Where("id = ? AND account_id = ?", projectID, accountID.(string)).Update("polar_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Polar API key"})
		return
	}

	s.invalidateAllCaches(projectID, accountID.(string))

	log.Printf("Polar API key updated for project %s by account %s", projectID, accountID)
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Polar API key updated successfully",
	})
}

// ---- Sync Handler ----

func (s *PolarService) SyncPolarDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getPolarAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Invalidate caches to force fresh fetch
	s.invalidateAllCaches(projectID, accountID.(string))

	// Test fetch to validate key still works
	_, err = s.polarRequest("GET", "/v1/products/?limit=1", apiKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to connect to Polar API: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Polar data cache cleared. Fresh data will be fetched on next request.",
	})
}

// ---- Revenue Metrics Handler ----

func (s *PolarService) GetRevenueMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getPolarAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check cache
	cacheKey := projectID + ":polar:metrics"
	if cached, ok := s.cache.get(cacheKey); ok {
		if metrics, ok := cached.(*PolarMetrics); ok {
			c.JSON(http.StatusOK, gin.H{"status": "success", "data": metrics})
			return
		}
	}

	orders, subscriptions, customers, refunds, err := s.fetchAllPolarData(apiKey)
	if err != nil {
		log.Printf("Warning: Polar data fetch had errors: %v", err)
	}

	metrics := s.computeMetrics(orders, subscriptions, customers, refunds)

	// Build time series for last 30 days
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -30)
	timeSeries := s.buildTimeSeries(orders, subscriptions, customers, startDate, endDate)

	// Build response matching the RevenueMetrics interface
	response := gin.H{
		"date":                        metrics.Date,
		"mrr":                         metrics.MRR,
		"arr":                         metrics.ARR,
		"total_revenue":               metrics.TotalRevenue,
		"active_subscriptions":        metrics.ActiveSubscriptions,
		"canceled_subscriptions":      metrics.CanceledSubscriptions,
		"new_subscriptions":           metrics.NewSubscriptions,
		"churned_subscriptions":       metrics.ChurnedSubscriptions,
		"expansion_revenue":           metrics.ExpansionRevenue,
		"contraction_revenue":         metrics.ContractionRevenue,
		"net_revenue":                 metrics.NetRevenue,
		"expansion_mrr":               metrics.ExpansionMRR,
		"downgrade_mrr":               metrics.DowngradeMRR,
		"churned_mrr":                 metrics.ChurnedMRR,
		"net_revenue_churn":           metrics.NetRevenueChurn,
		"churn_rate":                  metrics.ChurnRate,
		"growth_rate":                 metrics.GrowthRate,
		"arpu":                        metrics.ARPU,
		"customer_lifetime_value":     metrics.CustomerLifetimeValue,
		"trial_to_pay_conversion_rate": metrics.TrialToPayConversionRate,
		"total_customers":             metrics.TotalCustomers,
		"active_customers":            metrics.ActiveCustomers,
		"last_updated":                metrics.LastUpdated,
		"time_series":                 timeSeries,
		"date_range": gin.H{
			"start": startDate.Format("2006-01-02"),
			"end":   endDate.Format("2006-01-02"),
		},
	}

	s.cache.set(cacheKey, metrics, PolarCacheTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}

// ---- Revenue Analytics Handler ----

func (s *PolarService) GetRevenueAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getPolarAPIKey(projectID, accountID.(string))
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

	cacheKey := fmt.Sprintf("%s:polar:analytics:%s:%s", projectID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if cached, ok := s.cache.get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": cached})
		return
	}

	orders, subscriptions, customers, _, err := s.fetchAllPolarData(apiKey)
	if err != nil {
		log.Printf("Warning: Polar data fetch had errors: %v", err)
	}

	timeSeries := s.buildTimeSeries(orders, subscriptions, customers, startDate, endDate)

	// Compute summary from current state
	metrics := s.computeMetrics(orders, subscriptions, customers, nil)

	response := gin.H{
		"summary": gin.H{
			"current_mrr":          metrics.MRR,
			"current_arr":          metrics.ARR,
			"active_subscriptions": metrics.ActiveSubscriptions,
			"churn_rate":           metrics.ChurnRate,
			"arpu":                 metrics.ARPU,
			"trial_conversion_rate": metrics.TrialToPayConversionRate,
		},
		"time_series": timeSeries,
		"date_range": gin.H{
			"start": startDate.Format("2006-01-02"),
			"end":   endDate.Format("2006-01-02"),
		},
	}

	s.cache.set(cacheKey, response, PolarCacheTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}

// ---- Customer Analytics Handler ----

func (s *PolarService) GetCustomerAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	apiKey, err := s.getPolarAPIKey(projectID, accountID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cacheKey := projectID + ":polar:customers"
	if cached, ok := s.cache.get(cacheKey); ok {
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": cached})
		return
	}

	_, subscriptions, customers, _, err := s.fetchAllPolarData(apiKey)
	if err != nil {
		log.Printf("Warning: Polar data fetch had errors: %v", err)
	}

	// Build per-customer MRR map
	customerMRR := make(map[string]float64)
	customerSubCount := make(map[string]int)
	for _, sub := range subscriptions {
		if sub.Status == "active" {
			amount := float64(sub.Amount) / 100.0
			if sub.RecurringInterval == "year" {
				amount = amount / 12.0
			}
			customerMRR[sub.CustomerID] += amount
			customerSubCount[sub.CustomerID]++
		}
	}

	var customerInfos []PolarCustomerInfo
	var paidCount, freeCount int
	var totalMRR float64

	for _, c := range customers {
		mrr := customerMRR[c.ID]
		status := "free"
		if mrr > 0 {
			status = "active"
			paidCount++
		} else {
			freeCount++
		}
		totalMRR += mrr

		customerInfos = append(customerInfos, PolarCustomerInfo{
			ID:            c.ID,
			Email:         c.Email,
			Name:          c.Name,
			MRR:           math.Round(mrr*100) / 100,
			Status:        status,
			Created:       c.CreatedAt,
			Subscriptions: customerSubCount[c.ID],
		})
	}

	// Sort by MRR descending
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

	s.cache.set(cacheKey, response, PolarCustomersTTL)

	c.JSON(http.StatusOK, gin.H{"status": "success", "data": response})
}
