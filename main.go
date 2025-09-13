package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	"mentiq-backend/prisma/db"
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
}

type AnalyticsService struct {
	s3Client   *s3.S3
	bucketName string
	dbClient   *db.PrismaClient

	// Memory cache for events
	eventCache []Event
	cacheMutex sync.RWMutex

	// Flush ticker for periodic batch uploads
	flushTicker *time.Ticker
	stopChan    chan bool
}

func NewAnalyticsService(bucketName string, dbClient *db.PrismaClient) (*AnalyticsService, error) {
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
		s3Client:    s3.New(sess),
		bucketName:  bucketName,
		dbClient:    dbClient,
		eventCache:  make([]Event, 0),
		flushTicker: time.NewTicker(30 * time.Minute),
		stopChan:    make(chan bool),
	}

	// Start the background worker for periodic batch uploads
	go service.startBatchProcessor()

	return service, nil
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

// flushEventCache processes all cached events and uploads them to S3
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

	log.Printf("Flushing %d events from cache to S3", len(eventsToFlush))

	successCount := 0
	for _, event := range eventsToFlush {
		if err := as.storeEventToS3(event); err != nil {
			log.Printf("Failed to store cached event %s: %v", event.EventID, err)
		} else {
			successCount++
		}
	}

	log.Printf("Successfully flushed %d/%d events to S3", successCount, len(eventsToFlush))
}

// getCacheSize returns the current size of the event cache (thread-safe)
func (as *AnalyticsService) getCacheSize() int {
	as.cacheMutex.RLock()
	defer as.cacheMutex.RUnlock()
	return len(as.eventCache)
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

	as.flushEventCache()

	c.JSON(http.StatusOK, gin.H{
		"message":          "Cache flushed successfully",
		"events_processed": cacheSize,
		"new_cache_size":   as.getCacheSize(),
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

func main() {
	// Load environment variables from .env.local file
	if err := godotenv.Load(".env.local"); err != nil {
		log.Printf("Warning: Could not load .env.local file: %v", err)
		// Try loading from .env as fallback
		if err := godotenv.Load(".env"); err != nil {
			log.Printf("Warning: Could not load .env file: %v", err)
		}
	}

	// Initialize Prisma Client
	dbClient := db.NewClient()
	if err := dbClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer func() {
		if err := dbClient.Disconnect(); err != nil {
			panic(err)
		}
	}()

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
	analyticsService, err := NewAnalyticsService(bucketName, dbClient)
	if err != nil {
		log.Fatalf("Failed to initialize analytics service: %v", err)
	}

	// Setup Gin router
	router := gin.Default()

	// Setup CORS middleware
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	config.AllowCredentials = true

	router.Use(cors.New(config))

	// API routes
	apiV1 := router.Group("/api/v1")
	apiV1.Use(AuthMiddleware(dbClient)) // Apply auth middleware to all v1 routes
	{
		apiV1.POST("/events", analyticsService.ingestEventHandler)
		apiV1.POST("/events/batch", analyticsService.batchIngestHandler)
		apiV1.GET("/analytics", analyticsService.GetAnalyticsHandler)
		apiV1.GET("/dashboard", analyticsService.GetDashboardHandler)
		apiV1.GET("/realtime", analyticsService.GetRealTimeHandler)
		apiV1.POST("/flush-cache", analyticsService.flushCacheHandler) // Manual cache flush endpoint
	}

	// Public routes
	router.POST("/signup", signupHandler(dbClient))
	router.GET("/health", healthCheckHandler)

	// Serve static dashboard
	router.Static("/static", "./")
	router.GET("/", func(c *gin.Context) {
		c.File("./dashboard.html")
	})

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
