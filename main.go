package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	geoip2 "github.com/oschwald/geoip2-golang"
	"github.com/stripe/stripe-go/v72/client"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Event represents an analytics event stored in TimescaleDB hypertable
type Event struct {
	ID         uint                   `gorm:"primaryKey;autoIncrement" json:"-"` // Internal ID for GORM
	EventID    string                 `gorm:"type:uuid;uniqueIndex;not null" json:"event_id"`
	EventType  string                 `gorm:"index;not null" json:"event_type"`
	UserID     string                 `gorm:"index" json:"user_id,omitempty"`
	SessionID  string                 `gorm:"index" json:"session_id,omitempty"`
	Timestamp  time.Time              `gorm:"not null;index:idx_events_time" json:"timestamp"`
	Properties map[string]interface{} `gorm:"type:jsonb;serializer:json" json:"properties,omitempty"`
	UserAgent  string                 `json:"user_agent,omitempty"`
	IPAddress  string                 `json:"ip_address,omitempty"`
	AccountID  string                 `gorm:"index:idx_events_account_project;not null" json:"account_id"`
	ProjectID  string                 `gorm:"index:idx_events_account_project;not null" json:"project_id"`
	Country    string                 `gorm:"index" json:"country,omitempty"`
	City       string                 `json:"city,omitempty"`
	Device     string                 `gorm:"index" json:"device,omitempty"`
	OS         string                 `gorm:"index" json:"os,omitempty"`
	Browser    string                 `gorm:"index" json:"browser,omitempty"`
	Channel    string                 `gorm:"index" json:"channel,omitempty"`
	Email      string                 `gorm:"index" json:"email,omitempty"`
	CreatedAt  time.Time              `gorm:"autoCreateTime" json:"created_at"`
}

func (Event) TableName() string {
	return "events"
}

// CacheEntry represents a cached item with TTL
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

type AnalyticsService struct {
	db           *gorm.DB
	stripeClient *client.API
	stopChan     chan struct{}

	// Data caches with TTL
	eventsCache    map[string]*CacheEntry // Key: accountID:projectID:startDate:endDate
	dashboardCache map[string]*CacheEntry // Key: accountID:projectID:date
	metricsCache   map[string]*CacheEntry // Key: accountID:projectID:metric:date
	dataCacheMutex sync.RWMutex
}

func NewAnalyticsService(db *gorm.DB) (*AnalyticsService, error) {
	service := &AnalyticsService{
		db:             db,
		stripeClient:   NewStripeClient(),
		eventsCache:    make(map[string]*CacheEntry),
		dashboardCache: make(map[string]*CacheEntry),
		metricsCache:   make(map[string]*CacheEntry),
		stopChan:       make(chan struct{}),
	}

	// Start the background workers
	go service.startCacheCleanup() // Cache cleanup worker

	log.Println("Analytics service initialized with TimescaleDB storage")
	return service, nil
}

type Server struct {
	db                       *gorm.DB
	analyticsService         *AnalyticsService
	stripeService            *StripeService
	enhancedAnalyticsService *EnhancedAnalyticsService
	sessionStorage           *SessionStorageService
}

func NewServer(db *gorm.DB, analyticsService *AnalyticsService) *Server {
	stripeService := NewStripeService(db)
	enhancedAnalyticsService := NewEnhancedAnalyticsService(db)

	// Initialize session storage (optional - falls back to DB if not configured)
	sessionStorage, err := NewSessionStorageService()
	if err != nil {
		log.Printf("Session storage not configured: %v. Using database storage for recordings.", err)
		sessionStorage = nil
	}

	return &Server{
		db:                       db,
		analyticsService:         analyticsService,
		stripeService:            stripeService,
		enhancedAnalyticsService: enhancedAnalyticsService,
		sessionStorage:           sessionStorage,
	}
}

func (s *Server) Connect() error {
	// GORM already handles the connection, just verify it
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	return nil
}

func (s *Server) Disconnect() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

type SignupRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type CreateProjectRequest struct {
	Name string `json:"name" binding:"required"`
}

type CreateApiKeyRequest struct {
	Name        string   `json:"name" binding:"required"`
	Permissions []string `json:"permissions"`
}

type UpdateStripeApiKeyRequest struct {
	ApiKey string `json:"api_key" binding:"required"`
}

// JWTClaims represents the claims in a JWT token
type JWTClaims struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	ProjectID string `json:"project_id,omitempty"`
	APIKeyID  string `json:"api_key_id,omitempty"`
	Exp       int64  `json:"exp"` // Expiration time
	Iat       int64  `json:"iat"` // Issued at time
}

// GenerateJWT generates a JWT token for an account with HMAC-SHA256 signing
func GenerateJWT(accountID, email string, expiresInHours int) (string, error) {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return "", fmt.Errorf("JWT_SECRET must be configured")
	}

	now := time.Now().Unix()
	expiryTime := time.Now().Add(time.Duration(expiresInHours) * time.Hour).Unix()

	claims := JWTClaims{
		AccountID: accountID,
		Email:     email,
		Exp:       expiryTime,
		Iat:       now,
	}

	// Create header
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	// Encode header
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %v", err)
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Encode payload
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal claims: %v", err)
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Create signature
	message := headerEncoded + "." + payloadEncoded
	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Combine all parts
	token := message + "." + signature

	return token, nil
}

// ValidateJWT validates a JWT token and extracts claims
func ValidateJWT(tokenString string) (*JWTClaims, error) {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET must be configured")
	}

	// Split token into parts
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	headerEncoded := parts[0]
	payloadEncoded := parts[1]
	signatureEncoded := parts[2]

	// Verify signature
	message := headerEncoded + "." + payloadEncoded
	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(message))
	expectedSignature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(signatureEncoded), []byte(expectedSignature)) {
		return nil, fmt.Errorf("invalid token signature")
	}

	// Decode and parse payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return nil, fmt.Errorf("invalid token payload encoding: %v", err)
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid token payload: %v", err)
	}

	// Check expiration
	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	return &claims, nil
}

func main() {

	// Load environment variables from .env.local file
	if err := godotenv.Load(".env"); err != nil {
		log.Printf("Warning: Could not load .env.local file: %v", err)
		// Try loading from .env as fallback
		if err := godotenv.Load(".env"); err != nil {
			log.Printf("Warning: Could not load .env file: %v", err)
		}
	}

	// Initialize GORM database
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	database, err := InitDB(databaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Run migrations
	// if err := MigrateDB(database); err != nil {
	// 	log.Fatalf("Failed to run migrations: %v", err)
	// }

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize analytics service
	analyticsService, err := NewAnalyticsService(database)
	if err != nil {
		log.Fatalf("Failed to initialize analytics service: %v", err)
	}

	// Initialize server with database
	server := NewServer(database, analyticsService)
	if err := server.Connect(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer server.Disconnect()

	// Setup Gin router
	router := gin.Default()

	// Setup CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	config.AllowCredentials = true
	router.Use(cors.New(config))

	// Public routes
	router.POST("/signup", server.signupHandler)
	router.POST("/login", server.loginHandler)
	router.GET("/health", healthCheckHandler)

	// Serve static dashboard
	router.Static("/static", "./")
	router.GET("/", func(c *gin.Context) {
		c.File("./dashboard.html")
	})

	// API routes
	apiV1 := router.Group("/api/v1")

	apiV1.Use(server.authMiddleware) // Apply auth middleware to all v1 routes
	{
		apiV1.POST("/events", server.analyticsService.ingestEventHandler)
		apiV1.POST("/events/batch", server.analyticsService.batchIngestHandler)
		apiV1.GET("/analytics", server.analyticsService.GetAnalyticsHandler)
		apiV1.GET("/dashboard", server.analyticsService.GetDashboardHandler)
		apiV1.GET("/realtime", server.analyticsService.GetRealTimeHandler)
		apiV1.GET("/user-metrics", server.analyticsService.GetUserMetricsHandler) // New endpoint for DAU/WAU/MAU
		apiV1.POST("/flush-cache", server.analyticsService.flushCacheHandler)     // Manual cache flush endpoint
		apiV1.POST("/clear-cache", server.analyticsService.clearDataCacheHandler) // Manual data cache clear endpoint
		apiV1.GET("/analytics/heatmaps", server.analyticsService.GetHeatmapHandler)
		apiV1.GET("/analytics/errors", server.analyticsService.GetErrorAnalyticsHandler)
		apiV1.GET("/sessions/:session_id", server.analyticsService.GetSessionAnalyticsHandler)
		apiV1.GET("/analytics/retention", server.analyticsService.GetRetentionCohortsHandler)
		apiV1.GET("/analytics/churn-by-channel", server.analyticsService.GetChurnByChannelHandler)

		// Session Recording routes
		apiV1.POST("/sessions/:session_id/recordings", server.ingestRecordingHandler)
		apiV1.GET("/projects/:project_id/recordings", server.listRecordingsHandler)
		apiV1.GET("/projects/:project_id/recordings/:recordingId", server.getRecordingHandler)
		apiV1.GET("/projects/:project_id/sessions", server.getSessionsHandler)
		apiV1.GET("/projects/:project_id/sessions/:sessionId", server.getSessionHandler)

		// Heatmap routes (GET only - POST goes through /events endpoint)
		apiV1.GET("/projects/:project_id/heatmaps", server.analyticsService.GetHeatmapHandler)
		apiV1.GET("/projects/:project_id/heatmaps/pages", server.getHeatmapPagesHandler)

		// Project and API Key management
		apiV1.POST("/projects", server.createProjectHandler)
		apiV1.GET("/projects", server.listProjectsHandler)
		apiV1.GET("/projects/:project_id/events", server.analyticsService.GetEventsHandler)
		apiV1.POST("/projects/:project_id/apikeys", server.createApiKeyHandler)
		apiV1.GET("/projects/:project_id/apikeys", server.listApiKeysHandler)
		apiV1.PUT("/projects/:project_id/apikeys/:key_id", server.updateApiKeyHandler)
		apiV1.DELETE("/projects/:project_id/apikeys/:key_id", server.deleteApiKeyHandler)
		apiV1.PUT("/projects/:project_id/stripe-key", server.updateProjectStripeApiKeyHandler)

		// Stripe Revenue Analytics routes
		apiV1.POST("/projects/:project_id/stripe/sync", server.stripeService.SyncStripeDataHandler)
		apiV1.GET("/projects/:project_id/stripe/metrics", server.stripeService.GetRevenueMetricsHandler)
		apiV1.GET("/projects/:project_id/stripe/analytics", server.stripeService.GetRevenueAnalyticsHandler)
		apiV1.GET("/projects/:project_id/stripe/customers", server.stripeService.GetCustomerAnalyticsHandler)

		// Enhanced Analytics routes
		apiV1.GET("/projects/:project_id/analytics/location", server.enhancedAnalyticsService.LocationAnalyticsHandler)
		apiV1.GET("/projects/:project_id/analytics/devices", server.enhancedAnalyticsService.DeviceAnalyticsHandler)
		apiV1.GET("/projects/:project_id/analytics/cohorts", server.enhancedAnalyticsService.RetentionCohortHandler)
		apiV1.GET("/projects/:project_id/analytics/features", server.enhancedAnalyticsService.FeatureAdoptionHandler)
		apiV1.GET("/projects/:project_id/analytics/churn", server.enhancedAnalyticsService.ChurnRiskHandler)
		apiV1.GET("/projects/:project_id/analytics/funnels", server.enhancedAnalyticsService.ConversionFunnelHandler)
		apiV1.GET("/projects/:project_id/analytics/sessions", server.enhancedAnalyticsService.SessionAnalyticsHandler)

		// A/B Testing routes
		apiV1.POST("/projects/:project_id/experiments", server.CreateExperiment)
		apiV1.GET("/projects/:project_id/experiments", server.GetExperiments)
		apiV1.GET("/projects/:project_id/experiments/:experimentId", server.GetExperiment)
		apiV1.PATCH("/projects/:project_id/experiments/:experimentId", server.UpdateExperiment)
		apiV1.DELETE("/projects/:project_id/experiments/:experimentId", server.DeleteExperiment)
		apiV1.GET("/projects/:project_id/experiments/:experimentId/results", server.GetExperimentResults)

		// Global experiment routes (for SDK)
		apiV1.POST("/experiments/:experimentKey/assignment", server.GetAssignment)
		apiV1.POST("/experiments/track", server.TrackConversion)
		apiV1.PUT("/experiments/:id/status", server.UpdateExperimentStatus)
	}

	// Setup graceful shutdown
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Analytics platform server starting on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Gracefully stop the analytics service (flush cache)
	analyticsService.Stop()

	// Create a deadline to wait for
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited")
}

// These methods are no longer needed with TimescaleDB direct writes

// Cache helper functions

// generateCacheKey creates a cache key for the given parameters
func (as *AnalyticsService) generateCacheKey(cacheType, accountID, projectID string, params ...string) string {
	key := fmt.Sprintf("%s:%s:%s", cacheType, accountID, projectID)
	for _, param := range params {
		key += ":" + param
	}
	return key
}

// getCachedData retrieves data from cache if it exists and hasn't expired
func (as *AnalyticsService) getCachedData(cacheKey string, cacheMap map[string]*CacheEntry) (interface{}, bool) {
	as.dataCacheMutex.RLock()
	defer as.dataCacheMutex.RUnlock()

	entry, exists := cacheMap[cacheKey]
	if !exists {
		return nil, false
	}

	// Check if cache entry has expired
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Data, true
}

// setCachedData stores data in cache with TTL
func (as *AnalyticsService) setCachedData(cacheKey string, data interface{}, ttl time.Duration, cacheMap map[string]*CacheEntry) {
	as.dataCacheMutex.Lock()
	defer as.dataCacheMutex.Unlock()

	cacheMap[cacheKey] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// getCachedEvents retrieves events from cache
func (as *AnalyticsService) getCachedEvents(accountID, projectID, startDate, endDate string) ([]Event, bool) {
	cacheKey := as.generateCacheKey("events", accountID, projectID, startDate, endDate)
	data, found := as.getCachedData(cacheKey, as.eventsCache)
	if !found {
		return nil, false
	}
	return data.([]Event), true
}

// setCachedEvents stores events in cache (30 minutes TTL)
func (as *AnalyticsService) setCachedEvents(accountID, projectID, startDate, endDate string, events []Event) {
	cacheKey := as.generateCacheKey("events", accountID, projectID, startDate, endDate)
	as.setCachedData(cacheKey, events, 30*time.Minute, as.eventsCache)
}

// getCachedDashboard retrieves dashboard data from cache
func (as *AnalyticsService) getCachedDashboard(accountID, projectID string) (map[string]interface{}, bool) {
	cacheKey := as.generateCacheKey("dashboard", accountID, projectID, time.Now().Format("2006-01-02"))
	data, found := as.getCachedData(cacheKey, as.dashboardCache)
	if !found {
		return nil, false
	}
	return data.(map[string]interface{}), true
}

// setCachedDashboard stores dashboard data in cache (10 minutes TTL)
func (as *AnalyticsService) setCachedDashboard(accountID, projectID string, dashboard map[string]interface{}) {
	cacheKey := as.generateCacheKey("dashboard", accountID, projectID, time.Now().Format("2006-01-02"))
	as.setCachedData(cacheKey, dashboard, 10*time.Minute, as.dashboardCache)
}

// getCachedMetrics retrieves metrics from cache
func (as *AnalyticsService) getCachedMetrics(accountID, projectID, metric, date string) (interface{}, bool) {
	cacheKey := as.generateCacheKey("metrics", accountID, projectID, metric, date)
	return as.getCachedData(cacheKey, as.metricsCache)
}

// setCachedMetrics stores metrics in cache (15 minutes TTL)
func (as *AnalyticsService) setCachedMetrics(accountID, projectID, metric, date string, data interface{}) {
	cacheKey := as.generateCacheKey("metrics", accountID, projectID, metric, date)
	as.setCachedData(cacheKey, data, 15*time.Minute, as.metricsCache)
}

// startCacheCleanup runs a background goroutine that periodically removes expired cache entries
func (as *AnalyticsService) startCacheCleanup() {
	cleanupTicker := time.NewTicker(10 * time.Minute) // Clean every 10 minutes
	defer cleanupTicker.Stop()

	for {
		select {
		case <-cleanupTicker.C:
			as.cleanExpiredCache()
		case <-as.stopChan:
			return
		}
	}
}

// cleanExpiredCache removes expired entries from all caches
func (as *AnalyticsService) cleanExpiredCache() {
	as.dataCacheMutex.Lock()
	defer as.dataCacheMutex.Unlock()

	now := time.Now()
	cleaned := 0

	// Clean events cache
	for key, entry := range as.eventsCache {
		if now.After(entry.ExpiresAt) {
			delete(as.eventsCache, key)
			cleaned++
		}
	}

	// Clean dashboard cache
	for key, entry := range as.dashboardCache {
		if now.After(entry.ExpiresAt) {
			delete(as.dashboardCache, key)
			cleaned++
		}
	}

	// Clean metrics cache
	for key, entry := range as.metricsCache {
		if now.After(entry.ExpiresAt) {
			delete(as.metricsCache, key)
			cleaned++
		}
	}

	if cleaned > 0 {
		log.Printf("Cache cleanup: removed %d expired entries", cleaned)
	}
}

// Stop signals background workers to stop and flush if necessary
func (as *AnalyticsService) Stop() {
	select {
	case <-as.stopChan:
		// already closed
		return
	default:
		close(as.stopChan)
	}
}

// addEventToCache persists an event directly to TimescaleDB (no R2/S3 cache)
func (as *AnalyticsService) addEventToCache(event Event) {
	if err := as.db.Create(&event).Error; err != nil {
		log.Printf("Failed to persist event to TimescaleDB: %v", err)
		return
	}
}

// getCacheSize returns the current logical cache size (eventsCache length)
func (as *AnalyticsService) getCacheSize() int {
	as.dataCacheMutex.RLock()
	defer as.dataCacheMutex.RUnlock()
	return len(as.eventsCache)
}

// Global GeoIP database reader (initialized once)
var geoipDB *geoip2.Reader
var geoipOnce sync.Once

func getGeoLocation(ipAddress string) (string, string) {
	// Initialize GeoIP database once
	geoipOnce.Do(func() {
		mmdbPath := os.Getenv("GEOIP_MMDB_PATH")
		if mmdbPath == "" {
			mmdbPath = "./utils/GeoLite2-City.mmdb" // Default path
		}

		db, err := geoip2.Open(mmdbPath)
		if err != nil {
			log.Printf("Warning: Failed to open GeoIP database at %s: %v", mmdbPath, err)
			log.Printf("Geolocation will return default values. Download from: https://github.com/P3TERX/GeoLite.mmdb")
			return
		}
		geoipDB = db
		log.Printf("GeoIP database loaded successfully from: %s", mmdbPath)
	})

	// If GeoIP database is not available, return default
	if geoipDB == nil {
		return "Unknown", "Unknown"
	}

	// Parse IP address
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		log.Printf("Invalid IP address: %s", ipAddress)
		return "Unknown", "Unknown"
	}

	// Skip private/local IPs
	if ip.IsPrivate() || ip.IsLoopback() {
		return "Local", "Local"
	}

	// Query the database
	record, err := geoipDB.City(ip)
	if err != nil {
		log.Printf("Error looking up IP %s: %v", ipAddress, err)
		return "Unknown", "Unknown"
	}

	country := "Unknown"
	city := "Unknown"

	// Extract country
	if record.Country.Names != nil {
		if name, ok := record.Country.Names["en"]; ok && name != "" {
			country = name
		}
	}

	// Extract city
	if record.City.Names != nil {
		if name, ok := record.City.Names["en"]; ok && name != "" {
			city = name
		}
	}

	return country, city
}

func healthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "analytics-platform",
	})
}

// clearDataCacheHandler allows manual clearing of data caches (for testing/admin purposes)
func (as *AnalyticsService) clearDataCacheHandler(c *gin.Context) {
	as.dataCacheMutex.Lock()

	eventsCount := len(as.eventsCache)
	dashboardCount := len(as.dashboardCache)
	metricsCount := len(as.metricsCache)

	// Clear all caches
	as.eventsCache = make(map[string]*CacheEntry)
	as.dashboardCache = make(map[string]*CacheEntry)
	as.metricsCache = make(map[string]*CacheEntry)

	as.dataCacheMutex.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"message": "All data caches cleared successfully",
		"cleared": gin.H{
			"events_cache":    eventsCount,
			"dashboard_cache": dashboardCount,
			"metrics_cache":   metricsCount,
			"total":           eventsCount + dashboardCount + metricsCount,
		},
	})
}

func getClientIPFromGin(c *gin.Context) string {
	// Check for X-Forwarded-For header (common in load balancers)
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		// Take the first IP from the comma-separated list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check for X-Real-IP header
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Use Gin's ClientIP method as fallback
	return c.ClientIP()
}

func (s *Server) updateProjectStripeApiKeyHandler(c *gin.Context) {
	var req UpdateStripeApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	projectID := c.Param("project_id")

	if err := s.db.Model(&Project{}).Where("id = ?", projectID).Update("stripe_api_key", req.ApiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update Stripe API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Stripe API key updated successfully"})
}

// Session Recording Handlers

// ingestRecordingHandler receives and stores session recording data
// Stores events in S3/R2 if configured, otherwise falls back to DB JSONB storage
func (s *Server) ingestRecordingHandler(c *gin.Context) {
	sessionID := c.Param("session_id")

	// Parse request body
	var recordingData struct {
		Events    json.RawMessage `json:"events"`
		Duration  int             `json:"duration"`
		StartURL  string          `json:"start_url"`
		AccountID string          `json:"account_id"`
		ProjectID string          `json:"project_id"`
		UserID    *string         `json:"user_id"`
	}

	if err := c.ShouldBindJSON(&recordingData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Extract account/project from auth context if not in payload
	if recordingData.AccountID == "" {
		if accountID, exists := c.Get("account_id"); exists && accountID != nil {
			if accID, ok := accountID.(string); ok {
				recordingData.AccountID = accID
			}
		}
	}
	if recordingData.ProjectID == "" {
		if projectID, exists := c.Get("project_id"); exists && projectID != nil {
			if projID, ok := projectID.(string); ok {
				recordingData.ProjectID = projID
			}
		}
	}

	// Validate required fields
	if recordingData.AccountID == "" || recordingData.ProjectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id and project_id are required (authenticate or include in payload)"})
		return
	}

	// Generate unique ID for this recording
	recordingID := uuid.New().String()

	var storagePath string
	var eventsJSON []byte
	var err error

	// Try to upload to S3/R2 if configured
	if s.sessionStorage != nil {
		storagePath, err = s.sessionStorage.UploadRecording(
			sessionID,
			recordingData.ProjectID,
			recordingData.AccountID,
			recordingData.Events,
		)
		if err != nil {
			log.Printf("Failed to upload recording to S3: %v. Falling back to DB storage.", err)
			// Fall back to DB storage
			eventsJSON = recordingData.Events
			storagePath = "" // Empty storage path means data is in RecordingData field
		}
	} else {
		// No S3 configured, store in DB
		eventsJSON = recordingData.Events
	}

	// Calculate event count
	var eventCount int
	var eventsList []interface{}
	if err := json.Unmarshal(recordingData.Events, &eventsList); err == nil {
		eventCount = len(eventsList)
	}

	// Save or update recording metadata in database
	recording := SessionRecording{
		ID:            recordingID,
		SessionID:     sessionID,
		AccountID:     recordingData.AccountID,
		ProjectID:     recordingData.ProjectID,
		UserID:        recordingData.UserID,
		RecordingData: eventsJSON,  // Empty if stored in S3, populated if stored in DB
		StoragePath:   storagePath, // S3 key if stored in S3, empty if stored in DB
		Duration:      recordingData.Duration,
		StartURL:      recordingData.StartURL,
		EventCount:    eventCount,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Check if recording already exists and update, otherwise create
	var existingRecording SessionRecording
	err = s.db.Where("session_id = ? AND project_id = ?", sessionID, recordingData.ProjectID).First(&existingRecording).Error

	if err == nil {
		// Update existing recording
		recording.ID = existingRecording.ID
		recording.CreatedAt = existingRecording.CreatedAt
		if err := s.db.Save(&recording).Error; err != nil {
			log.Printf("Failed to update recording: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save recording"})
			return
		}
	} else {
		// Create new recording
		if err := s.db.Create(&recording).Error; err != nil {
			log.Printf("Failed to create recording: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save recording"})
			return
		}
	}

	storageType := "S3/R2"
	if storagePath == "" {
		storageType = "Database"
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Recording ingested successfully",
		"recording_id": recording.ID,
		"storage_type": storageType,
	})
}

// listRecordingsHandler returns a list of recordings for a project
func (s *Server) listRecordingsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id is required"})
		return
	}

	// Optional filters
	sessionID := c.Query("session_id")
	userID := c.Query("user_id")
	limit := 50
	offset := 0

	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Build query
	query := s.db.Model(&SessionRecording{}).Where("project_id = ?", projectID)

	if sessionID != "" {
		query = query.Where("session_id = ?", sessionID)
	}

	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}

	// Get total count
	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Printf("Failed to count recordings: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recordings"})
		return
	}

	// Get recordings
	var recordings []SessionRecording
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&recordings).Error; err != nil {
		log.Printf("Failed to fetch recordings: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recordings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recordings": recordings,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
	})
}

// getRecordingHandler returns a specific recording with its events
func (s *Server) getRecordingHandler(c *gin.Context) {
	recordingID := c.Param("recordingId")
	projectID := c.Param("project_id")

	// Fetch recording metadata from database and verify it belongs to the project
	var recording SessionRecording
	if err := s.db.Where("id = ? AND project_id = ?", recordingID, projectID).First(&recording).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
			return
		}
		log.Printf("Failed to fetch recording: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recording"})
		return
	}

	var events []map[string]interface{}
	var err error

	// Check if data is in S3 or DB
	if recording.StoragePath != "" && s.sessionStorage != nil {
		// Retrieve from S3/R2
		events, err = s.sessionStorage.DownloadRecording(recording.StoragePath)
		if err != nil {
			log.Printf("Failed to download recording from S3: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recording from storage"})
			return
		}
	} else if len(recording.RecordingData) > 0 {
		// Retrieve from DB
		if err := json.Unmarshal(recording.RecordingData, &events); err != nil {
			log.Printf("Failed to decode recording data from DB: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse recording events"})
			return
		}
	} else {
		// No recording data available
		c.JSON(http.StatusNotFound, gin.H{"error": "Recording events not found"})
		return
	}

	// Return recording with events
	c.JSON(http.StatusOK, gin.H{
		"id":          recording.ID,
		"session_id":  recording.SessionID,
		"account_id":  recording.AccountID,
		"project_id":  recording.ProjectID,
		"user_id":     recording.UserID,
		"duration":    recording.Duration,
		"start_url":   recording.StartURL,
		"event_count": recording.EventCount,
		"created_at":  recording.CreatedAt,
		"updated_at":  recording.UpdatedAt,
		"events":      events,
	})
}

// getSessionsHandler returns a list of sessions for a project
func (s *Server) getSessionsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id is required"})
		return
	}

	// Optional filters
	userID := c.Query("user_id")
	limit := 50
	offset := 0

	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// For now, get sessions from events - in a real implementation you might have a separate sessions table
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Fetch events to construct sessions
	events, err := s.analyticsService.fetchEventsForDateRange(accountID.(string), projectID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to fetch events for sessions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve sessions"})
		return
	}

	// Group events by session_id to create session summaries
	sessionMap := make(map[string]map[string]interface{})
	for _, event := range events {
		sessionID := event.SessionID
		if sessionID == "" {
			continue
		}

		if userID != "" && event.UserID != userID {
			continue
		}

		session, exists := sessionMap[sessionID]
		if !exists {
			session = map[string]interface{}{
				"id":         sessionID,
				"user_id":    event.UserID,
				"start_time": event.Timestamp,
				"end_time":   event.Timestamp,
				"events":     0,
				"page_views": 0,
				"device":     "Unknown",
				"browser":    "Unknown",
				"location":   "Unknown",
			}
			sessionMap[sessionID] = session
		}

		// Update session data
		session["events"] = session["events"].(int) + 1
		if event.EventType == "page_view" {
			session["page_views"] = session["page_views"].(int) + 1
		}

		// Update end time if this event is later
		if event.Timestamp.After(session["end_time"].(time.Time)) {
			session["end_time"] = event.Timestamp
		}

		// Update start time if this event is earlier
		if event.Timestamp.Before(session["start_time"].(time.Time)) {
			session["start_time"] = event.Timestamp
		}

		// Extract device info from event if available
		if event.Device != "" {
			session["device"] = event.Device
		}
		if event.Browser != "" {
			session["browser"] = event.Browser
		}
		if event.Country != "" {
			session["location"] = event.Country
		}
	}

	// Fetch recording durations for all sessions
	sessionIDs := make([]string, 0, len(sessionMap))
	for sessionID := range sessionMap {
		sessionIDs = append(sessionIDs, sessionID)
	}

	var recordings []SessionRecording
	if len(sessionIDs) > 0 {
		s.db.Where("session_id IN ? AND project_id = ?", sessionIDs, projectID).Find(&recordings)
	}

	// Create a map of session_id -> recording duration
	recordingDurations := make(map[string]int)
	for _, rec := range recordings {
		recordingDurations[rec.SessionID] = rec.Duration
	}

	// Convert to slice and set durations
	sessions := make([]map[string]interface{}, 0, len(sessionMap))
	for sessionID, session := range sessionMap {
		startTime := session["start_time"].(time.Time)
		endTime := session["end_time"].(time.Time)

		// Use recording duration if available, otherwise calculate from events
		if duration, exists := recordingDurations[sessionID]; exists && duration > 0 {
			session["duration"] = duration
		} else {
			session["duration"] = int(endTime.Sub(startTime).Seconds())
		}

		sessions = append(sessions, session)
	}

	// Sort by start time descending
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i]["start_time"].(time.Time).After(sessions[j]["start_time"].(time.Time))
	})

	// Apply pagination
	total := len(sessions)
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginatedSessions := sessions[start:end]

	c.JSON(http.StatusOK, gin.H{
		"sessions": paginatedSessions,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// getSessionHandler returns detailed information about a specific session
func (s *Server) getSessionHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	sessionID := c.Param("sessionId")

	if projectID == "" || sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id and session_id are required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Fetch events for this session
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")

	events, err := s.analyticsService.fetchEventsForDateRange(accountID.(string), projectID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to fetch events for session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve session"})
		return
	}

	// Filter events for this session
	sessionEvents := make([]Event, 0)
	for _, event := range events {
		if event.SessionID == sessionID {
			sessionEvents = append(sessionEvents, event)
		}
	}

	// Fetch recording for this session
	var recording SessionRecording
	if err := s.db.Where("session_id = ? AND project_id = ?", sessionID, projectID).First(&recording).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "No recording found for this session"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch session recording"})
		return
	}

	var rrwebEvents []map[string]interface{}

	// Check if data is in S3 or DB
	if recording.StoragePath != "" && s.sessionStorage != nil {
		// Retrieve from S3/R2
		data, err := s.sessionStorage.DownloadRecordingData(recording.StoragePath)
		if err != nil {
			log.Printf("Failed to retrieve recording from S3 for session %s: %v", sessionID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recording data"})
			return
		}
		if err := json.Unmarshal(data, &rrwebEvents); err != nil {
			log.Printf("Failed to unmarshal recording data for session %s: %v", sessionID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse recording data"})
			return
		}
	} else if len(recording.RecordingData) > 0 {
		// Retrieve from DB
		if err := json.Unmarshal(recording.RecordingData, &rrwebEvents); err != nil {
			log.Printf("Failed to unmarshal recording data from DB for session %s: %v", sessionID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse recording data from DB"})
			return
		}
	} else {
		// No recording data available
		rrwebEvents = []map[string]interface{}{}
	}

	// Fetch all analytics events for this session to build the summary
	var analyticsEvents []Event
	if err := s.db.Where("session_id = ? AND project_id = ?", sessionID, projectID).Order("timestamp asc").Find(&analyticsEvents).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events for session"})
		return
	}

	if len(analyticsEvents) == 0 && len(rrwebEvents) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found or has no events"})
		return
	}

	// Calculate session summary from analytics events
	var startTime, endTime time.Time
	var user_id string
	var country, city, device, os, browser string

	if len(analyticsEvents) > 0 {
		startTime = analyticsEvents[0].Timestamp
		endTime = analyticsEvents[len(analyticsEvents)-1].Timestamp
		user_id = analyticsEvents[0].UserID
		country = analyticsEvents[0].Country
		city = analyticsEvents[0].City
		device = analyticsEvents[0].Device
		os = analyticsEvents[0].OS
		browser = analyticsEvents[0].Browser
	} else if recording.CreatedAt != (time.Time{}) {
		startTime = recording.CreatedAt
		endTime = recording.CreatedAt.Add(time.Duration(recording.Duration) * time.Second)
	}

	// Use recording duration if available, otherwise calculate from analytics events
	duration := recording.Duration
	if duration == 0 && !startTime.IsZero() && !endTime.IsZero() {
		duration = int(endTime.Sub(startTime).Seconds())
	}

	// Respond with session details and the correct rrweb events
	c.JSON(http.StatusOK, gin.H{
		"id":         sessionID,
		"user_id":    user_id,
		"start_time": startTime,
		"end_time":   endTime,
		"duration":   duration,
		"events":     analyticsEvents, // Analytics events for timeline view
		"eventsList": rrwebEvents,     // RRWeb events for player
		"country":    country,
		"city":       city,
		"device":     device,
		"os":         os,
		"browser":    browser,
	})
}

// getHeatmapPagesHandler returns a list of pages with heatmap data
func (s *Server) getHeatmapPagesHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id is required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Fetch recent events to get page URLs
	startDate := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	endDate := time.Now().Format("2006-01-02")

	events, err := s.analyticsService.fetchEventsForDateRange(accountID.(string), projectID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to fetch events for heatmap pages: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve pages"})
		return
	}

	// Extract unique page URLs and count visits
	pageMap := make(map[string]map[string]interface{})
	for _, event := range events {
		if event.Properties == nil {
			continue
		}

		var url string
		if urlProp, exists := event.Properties["url"]; exists {
			if urlStr, ok := urlProp.(string); ok {
				url = urlStr
			}
		}

		if url == "" {
			continue
		}

		page, exists := pageMap[url]
		if !exists {
			page = map[string]interface{}{
				"id":           uuid.New().String(),
				"url":          url,
				"title":        url, // Default to URL, could extract from page_view events
				"visits":       0,
				"last_updated": event.Timestamp,
			}
			pageMap[url] = page
		}

		page["visits"] = page["visits"].(int) + 1
		if event.Timestamp.After(page["last_updated"].(time.Time)) {
			page["last_updated"] = event.Timestamp
		}

		// Try to get page title from properties
		if title, exists := event.Properties["title"]; exists {
			if titleStr, ok := title.(string); ok && titleStr != "" {
				page["title"] = titleStr
			}
		}
	}

	// Convert to slice and sort by visits descending
	pages := make([]map[string]interface{}, 0, len(pageMap))
	for _, page := range pageMap {
		pages = append(pages, page)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i]["visits"].(int) > pages[j]["visits"].(int)
	})

	c.JSON(http.StatusOK, pages)
}

func (s *Server) signupHandler(c *gin.Context) {
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if account already exists
	var existingAccount Account
	if err := s.db.Where("email = ?", req.Email).First(&existingAccount).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already in use"})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Create account
	account := Account{
		ID:       uuid.New().String(),
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
	}

	if err := s.db.Create(&account).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Account created successfully",
		"account": gin.H{
			"id":    account.ID,
			"name":  account.Name,
			"email": account.Email,
		},
	})
}

func (s *Server) loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Find account by email
	var account Account
	if err := s.db.Where("email = ?", req.Email).First(&account).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Generate JWT token (24 hour expiration)
	token, err := GenerateJWT(account.ID, account.Email, 24)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":    account.ID,
			"name":  account.Name,
			"email": account.Email,
		},
	})
}

func (s *Server) authMiddleware(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	projectID := c.GetHeader("X-Project-ID")

	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
		return
	}

	// Extract token
	token := strings.TrimPrefix(authHeader, "Bearer ")
	token = strings.TrimPrefix(token, "ApiKey ")

	// First, try to find by API key
	var apiKey APIKey
	if err := s.db.Where("key = ? AND is_active = ?", token, true).First(&apiKey).Error; err == nil {
		// Valid API key found - get the associated account through project
		var project Project
		if err := s.db.Where("id = ?", apiKey.ProjectID).First(&project).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}
		c.Set("account_id", project.AccountID)
		c.Set("project_id", apiKey.ProjectID)
		c.Set("api_key_id", apiKey.ID)
		c.Next()
		return
	}

	// Try to validate JWT token
	claims, err := ValidateJWT(token)
	if err == nil {
		// Valid JWT token
		c.Set("account_id", claims.AccountID)
		c.Set("email", claims.Email)
		if projectID != "" {
			c.Set("project_id", projectID)
		}
		c.Next()
		return
	}

	// If JWT validation failed, try email token (backward compatibility)
	var account Account
	if err := s.db.Where("email = ?", token).First(&account).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	c.Set("account_id", account.ID)
	c.Set("email", account.Email)
	c.Set("project_id", projectID)
	c.Next()
}

func (s *Server) listProjectsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")

	var projects []Project
	if err := s.db.Where("account_id = ?", accountID.(string)).Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}

	// Convert to response format
	var response []gin.H
	for _, project := range projects {
		response = append(response, gin.H{
			"id":        project.ID,
			"name":      project.Name,
			"accountId": project.AccountID,
			"createdAt": project.CreatedAt,
			"updatedAt": project.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) createProjectHandler(c *gin.Context) {
	var req CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	accountID, _ := c.Get("account_id")

	project := Project{
		ID:        uuid.New().String(),
		Name:      req.Name,
		AccountID: accountID.(string),
	}

	if err := s.db.Create(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        project.ID,
		"name":      project.Name,
		"accountId": project.AccountID,
		"createdAt": project.CreatedAt,
		"updatedAt": project.UpdatedAt,
	})
}

func (s *Server) createApiKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req CreateApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Create API key
	apiKey := APIKey{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Key:         "mentiq_live_" + uuid.New().String(),
		IsActive:    true,
		ProjectID:   projectID,
		Permissions: req.Permissions,
	}

	if err := s.db.Create(&apiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create API key"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":          apiKey.ID,
		"name":        apiKey.Name,
		"key":         apiKey.Key,
		"permissions": apiKey.Permissions,
		"isActive":    apiKey.IsActive,
		"projectId":   apiKey.ProjectID,
		"createdAt":   apiKey.CreatedAt,
	})
}

func (s *Server) listApiKeysHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var apiKeys []APIKey
	if err := s.db.Where("project_id = ?", projectID).Find(&apiKeys).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch API keys"})
		return
	}

	var result []gin.H
	for _, key := range apiKeys {
		result = append(result, gin.H{
			"id":          key.ID,
			"name":        key.Name,
			"key":         key.Key,
			"permissions": key.Permissions,
			"isActive":    key.IsActive,
			"projectId":   key.ProjectID,
			"createdAt":   key.CreatedAt,
			"updatedAt":   key.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) updateApiKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	keyID := c.Param("key_id")
	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var updateReq struct {
		Name        *string  `json:"name"`
		Permissions []string `json:"permissions"`
		IsActive    *bool    `json:"isActive"`
	}

	if err := c.ShouldBindJSON(&updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var apiKey APIKey
	if err := s.db.Where("id = ? AND project_id = ?", keyID, projectID).First(&apiKey).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		return
	}

	// Update fields if provided
	updates := make(map[string]interface{})
	if updateReq.Name != nil {
		updates["name"] = *updateReq.Name
	}
	if updateReq.Permissions != nil {
		updates["permissions"] = updateReq.Permissions
	}
	if updateReq.IsActive != nil {
		updates["is_active"] = *updateReq.IsActive
	}

	if len(updates) > 0 {
		if err := s.db.Model(&apiKey).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update API key"})
			return
		}
	}

	// Fetch the updated key
	if err := s.db.Where("id = ?", keyID).First(&apiKey).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch updated API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          apiKey.ID,
		"name":        apiKey.Name,
		"key":         apiKey.Key,
		"permissions": apiKey.Permissions,
		"isActive":    apiKey.IsActive,
		"projectId":   apiKey.ProjectID,
		"createdAt":   apiKey.CreatedAt,
		"updatedAt":   apiKey.UpdatedAt,
	})
}

func (s *Server) deleteApiKeyHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	keyID := c.Param("key_id")
	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Delete the API key
	result := s.db.Where("id = ? AND project_id = ?", keyID, projectID).Delete(&APIKey{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete API key"})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "API key deleted successfully"})
}

func (s *Server) dashboardHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")

	// Get real project count
	var projectCount int64
	if err := s.db.Model(&Project{}).Where("account_id = ?", accountID.(string)).Count(&projectCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch dashboard data"})
		return
	}

	// Get real API key count
	var apiKeyCount int64
	if err := s.db.Model(&APIKey{}).
		Joins("INNER JOIN project ON api_key.project_id = project.id").
		Where("project.account_id = ?", accountID.(string)).
		Count(&apiKeyCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch dashboard data"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"overview": gin.H{
			"totalProjects": projectCount,
			"totalApiKeys":  apiKeyCount,
			"activeUsers":   234, // Mock data for now
		},
	})
}
