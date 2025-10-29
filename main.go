package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/mileusna/useragent"
	"github.com/stripe/stripe-go/v72/client"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Event struct {
	EventID    string                 `json:"event_id"`
	EventType  string                 `json:"event_type"`
	UserID     string                 `json:"user_id,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	UserAgent  string                 `json:"user_agent,omitempty"`
	IPAddress  string                 `json:"ip_address,omitempty"`
	AccountID  string                 `json:"account_id"`
	ProjectID  string                 `json:"project_id"`
	Country    string                 `json:"country,omitempty"`
	City       string                 `json:"city,omitempty"`
	Device     string                 `json:"device,omitempty"`
	OS         string                 `json:"os,omitempty"`
	Browser    string                 `json:"browser,omitempty"`
}

// CacheEntry represents a cached item with TTL
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

type AnalyticsService struct {
	s3Client     *s3.S3
	bucketName   string
	db           *gorm.DB
	stripeClient *client.API

	// Memory cache for events
	eventCache []Event
	cacheMutex sync.RWMutex

	// Data caches with TTL
	eventsCache    map[string]*CacheEntry // Key: accountID:projectID:startDate:endDate
	dashboardCache map[string]*CacheEntry // Key: accountID:projectID:date
	metricsCache   map[string]*CacheEntry // Key: accountID:projectID:metric:date
	dataCacheMutex sync.RWMutex

	// Flush ticker for periodic batch uploads
	flushTicker *time.Ticker
	stopChan    chan bool
}

func NewAnalyticsService(bucketName string, db *gorm.DB) (*AnalyticsService, error) {
	// Configuration for Cloudflare R2
	config := &aws.Config{
		Region:           aws.String("auto"), // R2 uses "auto" as region
		Endpoint:         aws.String("https://820b251b57951011c6bcc9add6ca5ca4.r2.cloudflarestorage.com"),
		S3ForcePathStyle: aws.Bool(true), // Required for R2
	}

	// Set credentials from environment variables
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	if accessKey != "" && secretKey != "" {
		config.Credentials = credentials.NewStaticCredentials(
			accessKey,
			secretKey,
			"", // token (empty for R2)
		)
	} else {
		log.Printf("Warning: AWS credentials not found in environment variables")
	}

	sess, err := session.NewSession(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create R2 session: %v", err)
	}

	service := &AnalyticsService{
		s3Client:       s3.New(sess),
		bucketName:     bucketName,
		db:             db,
		stripeClient:   NewStripeClient(),
		eventCache:     make([]Event, 0),
		eventsCache:    make(map[string]*CacheEntry),
		dashboardCache: make(map[string]*CacheEntry),
		metricsCache:   make(map[string]*CacheEntry),
		flushTicker:    time.NewTicker(30 * time.Minute),
		stopChan:       make(chan bool),
	}

	// Start the background workers
	go service.startBatchProcessor()
	go service.startCacheCleanup() // New cache cleanup worker

	return service, nil
}

type Server struct {
	db               *gorm.DB
	analyticsService *AnalyticsService
}

func NewServer(db *gorm.DB, analyticsService *AnalyticsService) *Server {
	return &Server{db: db, analyticsService: analyticsService}
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
}

// GenerateJWT generates a JWT token for an account
func GenerateJWT(accountID, email string, expiresInHours int) (string, error) {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "your-secret-key-change-in-production" // Default for development
	}

	// Create a simple JWT-like token using base64 encoding
	// In production, use a proper JWT library
	expiryTime := time.Now().Add(time.Duration(expiresInHours) * time.Hour).Unix()
	tokenString := fmt.Sprintf("%s.%s.%d",
		accountID,
		email,
		expiryTime,
	)
	return tokenString, nil
}

// ValidateJWT validates a JWT token and extracts claims
func ValidateJWT(tokenString string) (*JWTClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	claims := &JWTClaims{
		AccountID: parts[0],
		Email:     parts[1],
	}

	// Validate expiration
	expiryUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid token expiration")
	}

	if time.Now().Unix() > expiryUnix {
		return nil, fmt.Errorf("token expired")
	}

	return claims, nil
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
	if err := MigrateDB(database); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Get configuration from environment variables
	bucketName := os.Getenv("S3_BUCKET_NAME")
	if bucketName == "" {
		log.Fatal("S3_BUCKET_NAME environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize analytics service
	analyticsService, err := NewAnalyticsService(bucketName, database)
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

		// Project and API Key management
		apiV1.POST("/projects", server.createProjectHandler)
		apiV1.GET("/projects", server.listProjectsHandler)
		apiV1.POST("/projects/:project_id/apikeys", server.createApiKeyHandler)
		apiV1.PUT("/projects/:project_id/stripe-key", server.updateProjectStripeApiKeyHandler)

		// A/B Testing routes
		apiV1.POST("/experiments", server.CreateExperiment)
		apiV1.GET("/experiments", server.GetExperiments)
		apiV1.GET("/experiments/:id", server.GetExperiment)
		apiV1.POST("/experiments/:experimentKey/assignment", server.GetAssignment)
		apiV1.POST("/experiments/track", server.TrackConversion)
		apiV1.GET("/experiments/:id/results", server.GetExperimentResults)
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
		log.Printf("S3 bucket: %s", bucketName)
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

// startBatchProcessor runs a background goroutine that periodically flushes the event cache
func (as *AnalyticsService) startBatchProcessor() {
	for {
		select {
		case <-as.flushTicker.C:
			as.flushEventCache()
		case <-as.stopChan:
			as.flushTicker.Stop()
			as.flushEventCache() // Final flush before stopping
			return
		}
	}
}

// Stop gracefully stops the analytics service
func (as *AnalyticsService) Stop() {
	close(as.stopChan)
}

// addEventToCache adds an event to the memory cache
func (as *AnalyticsService) addEventToCache(event Event) {
	as.cacheMutex.Lock()
	defer as.cacheMutex.Unlock()
	as.eventCache = append(as.eventCache, event)
	log.Printf("Event %s added to cache. Cache size: %d", event.EventID, len(as.eventCache))
}

// flushEventCache processes all cached events and uploads them to S3 in batches
func (as *AnalyticsService) flushEventCache() {
	as.cacheMutex.Lock()
	if len(as.eventCache) == 0 {
		as.cacheMutex.Unlock()
		return
	}

	eventsToFlush := make([]Event, len(as.eventCache))
	copy(eventsToFlush, as.eventCache)
	as.eventCache = as.eventCache[:0] // Clear the cache
	as.cacheMutex.Unlock()

	log.Printf("Flushing %d events from cache to S3 in batches", len(eventsToFlush))

	// Group events by account/project/date for efficient batching
	eventGroups := make(map[string][]Event)
	for _, event := range eventsToFlush {
		key := fmt.Sprintf("%s/%s/%s",
			event.AccountID,
			event.ProjectID,
			event.Timestamp.Format("2006-01-02"))
		eventGroups[key] = append(eventGroups[key], event)
	}

	var totalSuccess, totalFailed int
	var wg sync.WaitGroup

	// Process each group in parallel
	for groupKey, events := range eventGroups {
		wg.Add(1)
		go func(key string, eventBatch []Event) {
			defer wg.Done()
			success, failed := as.storeBatchToS3(eventBatch)
			totalSuccess += success
			totalFailed += failed
			log.Printf("Batch %s: %d success, %d failed", key, success, failed)
		}(groupKey, events)
	}

	wg.Wait()
	log.Printf("Batch flush completed: %d successful, %d failed out of %d total events",
		totalSuccess, totalFailed, len(eventsToFlush))
}

// storeBatchToS3 stores a batch of events as a single JSON array file in S3
func (as *AnalyticsService) storeBatchToS3(events []Event) (int, int) {
	if len(events) == 0 {
		return 0, 0
	}

	// Use the first event to determine the S3 path
	firstEvent := events[0]

	// Create a batch file with timestamp
	batchID := uuid.New().String()
	timestamp := time.Now().UTC()

	// Create S3 key for the batch file
	key := fmt.Sprintf("events/account_id=%s/project_id=%s/year=%d/month=%02d/day=%02d/batch_%s_%d_events.json",
		firstEvent.AccountID,
		firstEvent.ProjectID,
		firstEvent.Timestamp.Year(),
		firstEvent.Timestamp.Month(),
		firstEvent.Timestamp.Day(),
		batchID,
		len(events),
	)

	// Create batch payload
	batchPayload := map[string]interface{}{
		"batch_id":    batchID,
		"batch_size":  len(events),
		"uploaded_at": timestamp,
		"events":      events,
	}

	// Marshal to JSON
	batchJSON, err := json.Marshal(batchPayload)
	if err != nil {
		log.Printf("Failed to marshal batch: %v", err)
		return 0, len(events)
	}

	// Upload to S3
	_, err = as.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(as.bucketName),
		Key:         aws.String(key),
		Body:        aws.ReadSeekCloser(strings.NewReader(string(batchJSON))),
		ContentType: aws.String("application/json"),
		Metadata: map[string]*string{
			"batch-id":    aws.String(batchID),
			"event-count": aws.String(fmt.Sprintf("%d", len(events))),
			"account-id":  aws.String(firstEvent.AccountID),
			"project-id":  aws.String(firstEvent.ProjectID),
		},
	})

	if err != nil {
		log.Printf("Failed to store batch to S3: %v", err)
		return 0, len(events)
	}

	log.Printf("Batch %s with %d events stored to S3 with key: %s", batchID, len(events), key)
	return len(events), 0
}

// getCacheSize returns the current size of the event cache (thread-safe)
func (as *AnalyticsService) getCacheSize() int {
	as.cacheMutex.RLock()
	defer as.cacheMutex.RUnlock()
	return len(as.eventCache)
}

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

func (as *AnalyticsService) storeEventToS3(event Event) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	// Create S3 key with account, project, and date partitioning
	key := fmt.Sprintf("events/account_id=%s/project_id=%s/year=%d/month=%02d/day=%02d/%s.json",
		event.AccountID,
		event.ProjectID,
		event.Timestamp.Year(),
		event.Timestamp.Month(),
		event.Timestamp.Day(),
		event.EventID,
	)

	_, err = as.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(as.bucketName),
		Key:         aws.String(key),
		Body:        aws.ReadSeekCloser(strings.NewReader(string(eventJSON))),
		ContentType: aws.String("application/json"),
	})

	if err != nil {
		return fmt.Errorf("failed to store event to S3: %v", err)
	}

	log.Printf("Event %s stored to S3 with key: %s", event.EventID, key)
	return nil
}

func (as *AnalyticsService) ingestEventHandler(c *gin.Context) {
	var event Event
	if err := c.ShouldBindJSON(&event); err != nil {
		log.Printf("Failed to decode event: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract account and project from context (set by middleware)
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	event.AccountID = accountID.(string)
	event.ProjectID = projectID.(string)

	// Generate event ID if not provided
	if event.EventID == "" {
		event.EventID = uuid.New().String()
	}

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Extract client info
	event.UserAgent = c.GetHeader("User-Agent")
	event.IPAddress = getClientIPFromGin(c)

	// Parse User-Agent
	ua := useragent.Parse(event.UserAgent)
	event.Browser = ua.Name
	event.OS = ua.OS
	event.Device = ua.Device

	// Get Geo Location (placeholder)
	country, city := getGeoLocation(event.IPAddress)
	event.Country = country
	event.City = city

	// Validate required fields
	if event.EventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_type is required"})
		return
	}

	// Add event to cache instead of directly storing to S3
	as.addEventToCache(event)

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"event_id":   event.EventID,
		"message":    "Event queued for processing",
		"cache_size": as.getCacheSize(),
	})
}

func getGeoLocation(ipAddress string) (string, string) {
	// In a real application, you would use a service like MaxMind GeoIP
	// to get the location from the IP address.
	// For this example, we'll return dummy data.
	return "United States", "New York"
}

func (as *AnalyticsService) batchIngestHandler(c *gin.Context) {
	var events []Event
	if err := c.ShouldBindJSON(&events); err != nil {
		log.Printf("Failed to decode events: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract account and project from context (set by middleware)
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	var successCount int
	var errors []string

	for i, event := range events {
		event.AccountID = accountID.(string)
		event.ProjectID = projectID.(string)

		// Generate event ID if not provided
		if event.EventID == "" {
			event.EventID = uuid.New().String()
		}

		// Set timestamp if not provided
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}

		// Extract client info
		event.UserAgent = c.GetHeader("User-Agent")
		event.IPAddress = getClientIPFromGin(c)

		// Validate required fields
		if event.EventType == "" {
			errors = append(errors, fmt.Sprintf("Event %d: event_type is required", i))
			continue
		}

		// Add event to cache instead of directly storing to S3
		as.addEventToCache(event)
		successCount++
	}

	// Return response
	response := gin.H{
		"status":        "completed",
		"total_events":  len(events),
		"success_count": successCount,
		"error_count":   len(errors),
		"cache_size":    as.getCacheSize(),
	}

	if len(errors) > 0 {
		response["errors"] = errors
	}

	c.JSON(http.StatusOK, response)
}

func healthCheckHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "analytics-platform",
	})
}

// flushCacheHandler allows manual triggering of cache flush (for testing/admin purposes)
func (as *AnalyticsService) flushCacheHandler(c *gin.Context) {
	cacheSize := as.getCacheSize()
	if cacheSize == 0 {
		c.JSON(http.StatusOK, gin.H{
			"message":    "Cache is empty, nothing to flush",
			"cache_size": 0,
		})
		return
	}

	// Use a goroutine to flush cache asynchronously for large caches
	if cacheSize > 100 {
		go as.flushEventCache()
		c.JSON(http.StatusOK, gin.H{
			"message":           "Cache flush started asynchronously",
			"events_to_process": cacheSize,
			"status":            "processing",
		})
		return
	}

	// For smaller caches, flush synchronously
	as.flushEventCache()

	c.JSON(http.StatusOK, gin.H{
		"message":          "Cache flushed successfully",
		"events_processed": cacheSize,
		"new_cache_size":   as.getCacheSize(),
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

func getClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header (common in load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP from the comma-separated list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check for X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
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

func (s *Server) analyticsHandler(c *gin.Context) {
	// Mock analytics data - in a real implementation, you'd query the database
	c.JSON(http.StatusOK, gin.H{
		"totalEvents":        15420,
		"uniqueUsers":        3284,
		"sessions":           8921,
		"bounceRate":         32.5,
		"avgSessionDuration": 245,
		"topPages": []map[string]interface{}{
			{"page": "/dashboard", "views": 3421},
			{"page": "/projects", "views": 2156},
			{"page": "/analytics", "views": 1876},
		},
		"eventsOverTime": []map[string]interface{}{
			{"date": "2024-01-20", "count": 1200},
			{"date": "2024-01-21", "count": 1450},
			{"date": "2024-01-22", "count": 1680},
		},
	})
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

func (s *Server) realtimeHandler(c *gin.Context) {
	// Mock real-time data - in a real implementation, you'd use WebSockets or server-sent events
	c.JSON(http.StatusOK, gin.H{
		"activeUsers": 42,
		"recentEvents": []map[string]interface{}{
			{
				"id":        uuid.New().String(),
				"type":      "page_view",
				"timestamp": time.Now().Format(time.RFC3339),
				"userId":    "user_123",
				"page":      "/dashboard",
			},
		},
	})
}
