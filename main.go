package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
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
}

type AnalyticsService struct {
	s3Client   *s3.S3
	bucketName string
}

func NewAnalyticsService(bucketName string) (*AnalyticsService, error) {
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

	return &AnalyticsService{
		s3Client:   s3.New(sess),
		bucketName: bucketName,
	}, nil
}

func (as *AnalyticsService) storeEventToS3(event Event) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	// Create S3 key with date partitioning
	key := fmt.Sprintf("events/year=%d/month=%02d/day=%02d/%s.json",
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

	// Store event to S3
	if err := as.storeEventToS3(event); err != nil {
		log.Printf("Failed to store event: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Return success response
	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"event_id": event.EventID,
		"message":  "Event ingested successfully",
	})
}

func (as *AnalyticsService) batchIngestHandler(c *gin.Context) {
	var events []Event
	if err := c.ShouldBindJSON(&events); err != nil {
		log.Printf("Failed to decode events: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	var successCount int
	var errors []string

	for i, event := range events {
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

		// Store event to S3
		if err := as.storeEventToS3(event); err != nil {
			log.Printf("Failed to store event %d: %v", i, err)
			errors = append(errors, fmt.Sprintf("Event %d: failed to store", i))
			continue
		}

		successCount++
	}

	// Return response
	response := gin.H{
		"status":        "completed",
		"total_events":  len(events),
		"success_count": successCount,
		"error_count":   len(errors),
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
	analyticsService, err := NewAnalyticsService(bucketName)
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

	// Serve static dashboard
	router.Static("/static", "./")
	router.GET("/", func(c *gin.Context) {
		c.File("./dashboard.html")
	})

	// API routes
	router.GET("/health", healthCheckHandler)
	router.POST("/api/v1/events", analyticsService.ingestEventHandler)
	router.POST("/api/v1/events/batch", analyticsService.batchIngestHandler)

	// Analytics endpoints
	router.GET("/api/v1/analytics", analyticsService.GetAnalyticsHandler)
	router.GET("/api/v1/dashboard", analyticsService.GetDashboardHandler)
	router.GET("/api/v1/realtime", analyticsService.GetRealTimeHandler)

	// Start server
	log.Printf("Analytics platform server starting on port %s", port)
	log.Printf("S3 bucket: %s", bucketName)
	log.Fatal(router.Run(":" + port))
}
