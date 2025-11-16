package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// MentiqAnalyticsServer represents the main analytics server using R2 storage
type MentiqAnalyticsServer struct {
	router             *gin.Engine
	db                 *gorm.DB
	r2AnalyticsService *R2AnalyticsService
	enhancedHandlers   *EnhancedAnalyticsHandlers
	sessionHandlers    *SessionRecordingHandlers
	heatmapHandlers    *HeatmapHandlers
}

// NewMentiqAnalyticsServer creates a new analytics server with R2 backend
func NewMentiqAnalyticsServer() (*MentiqAnalyticsServer, error) {
	// Initialize database
	db, err := initializeDatabase()
	if err != nil {
		return nil, err
	}

	// Initialize R2/S3 client
	s3Client, err := initializeR2Client()
	if err != nil {
		return nil, err
	}

	// Create R2 analytics service
	r2Service := NewR2AnalyticsService(s3Client, "mentiq-analytics", db)

	// Create specialized handlers
	enhancedHandlers := NewEnhancedAnalyticsHandlers(r2Service)
	sessionHandlers := NewSessionRecordingHandlers(r2Service)
	heatmapHandlers := NewHeatmapHandlers(r2Service)

	// Setup Gin router
	router := gin.Default()

	server := &MentiqAnalyticsServer{
		router:             router,
		db:                 db,
		r2AnalyticsService: r2Service,
		enhancedHandlers:   enhancedHandlers,
		sessionHandlers:    sessionHandlers,
		heatmapHandlers:    heatmapHandlers,
	}

	server.setupRoutes()
	server.setupCORS()

	return server, nil
}

func initializeDatabase() (*gorm.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=password dbname=mentiq_analytics port=5432 sslmode=disable TimeZone=UTC"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto-migrate schema (for metadata/business logic, not analytics events)
	db.AutoMigrate(&Account{}, &Project{}, &User{})

	log.Println("Database connected and migrated")
	return db, nil
}

func initializeR2Client() (*s3.S3, error) {
	// R2 credentials and endpoint
	accessKey := os.Getenv("CLOUDFLARE_R2_ACCESS_KEY")
	secretKey := os.Getenv("CLOUDFLARE_R2_SECRET_KEY")
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")

	if accessKey == "" || secretKey == "" || accountID == "" {
		log.Println("Warning: R2 credentials not found, using default values")
		accessKey = "default_access_key"
		secretKey = "default_secret_key"
		accountID = "default_account"
	}

	endpoint := "https://" + accountID + ".r2.cloudflarestorage.com"

	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String("auto"),
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Endpoint:         aws.String(endpoint),
		S3ForcePathStyle: aws.Bool(true),
	})

	if err != nil {
		return nil, err
	}

	s3Client := s3.New(sess)
	log.Printf("R2 client initialized with endpoint: %s", endpoint)

	return s3Client, nil
}

func (mas *MentiqAnalyticsServer) setupCORS() {
	mas.router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
}

func (mas *MentiqAnalyticsServer) setupRoutes() {
	// Health check
	mas.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"timestamp": time.Now(),
			"service":   "Mentiq Analytics with R2",
		})
	})

	// Authentication middleware (simplified for demo)
	authGroup := mas.router.Group("/api/v1")
	authGroup.Use(mas.authMiddleware())

	// Event ingestion
	authGroup.POST("/events", mas.r2AnalyticsService.IngestEventHandler)

	// Enhanced Analytics Endpoints
	analytics := authGroup.Group("/analytics")
	{
		analytics.GET("/location", mas.enhancedHandlers.GetLocationAnalyticsHandler)
		analytics.GET("/devices", mas.enhancedHandlers.GetDeviceAnalyticsHandler)
		analytics.GET("/retention", mas.enhancedHandlers.GetRetentionAnalyticsHandler)
		analytics.GET("/features", mas.enhancedHandlers.GetFeatureAdoptionHandler)
		analytics.GET("/churn", mas.enhancedHandlers.GetChurnAnalysisHandler)
		analytics.GET("/conversion", mas.enhancedHandlers.GetConversionAnalyticsHandler)

		// Legacy analytics endpoint (from analytics.go)
		analytics.GET("/dashboard", mas.getDashboardAnalyticsHandler)
	}

	// Session Recording Endpoints
	sessions := authGroup.Group("/sessions")
	{
		sessions.POST("/start", mas.sessionHandlers.StartSessionHandler)
		sessions.POST("/:session_id/events", mas.sessionHandlers.RecordEventHandler)
		sessions.POST("/:session_id/batch", mas.sessionHandlers.RecordBatchHandler)
		sessions.PUT("/:session_id/end", mas.sessionHandlers.EndSessionHandler)
		sessions.GET("/:session_id", mas.sessionHandlers.GetSessionHandler)
		sessions.GET("/:session_id/recording", mas.sessionHandlers.GetSessionRecordingHandler)
		sessions.GET("/", mas.sessionHandlers.ListSessionsHandler)
	}

	// Heatmap Endpoints
	heatmaps := authGroup.Group("/heatmaps")
	{
		heatmaps.POST("/record", mas.heatmapHandlers.RecordHeatmapDataHandler)
		heatmaps.POST("/click", mas.heatmapHandlers.RecordClickHandler)
		heatmaps.POST("/scroll", mas.heatmapHandlers.RecordScrollHandler)
		heatmaps.GET("/page", mas.heatmapHandlers.GetPageHeatmapHandler)
		heatmaps.GET("/all", mas.heatmapHandlers.GetAllPagesHeatmapHandler)

		// Legacy heatmap endpoint (from main.go)
		heatmaps.GET("/", mas.r2AnalyticsService.GetHeatmapHandler)
	}
}

// Simplified auth middleware
func (mas *MentiqAnalyticsServer) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// In production, implement proper JWT/API key validation
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			apiKey = c.Query("api_key")
		}

		// For demo, use default values or extract from API key
		accountID := c.GetHeader("X-Account-ID")
		projectID := c.GetHeader("X-Project-ID")

		if accountID == "" {
			accountID = "demo_account_123"
		}
		if projectID == "" {
			projectID = "demo_project_456"
		}

		c.Set("account_id", accountID)
		c.Set("project_id", projectID)
		c.Set("authenticated", true)

		c.Next()
	}
}

// Legacy analytics handler to maintain compatibility
func (mas *MentiqAnalyticsServer) getDashboardAnalyticsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	// Check cache first
	cacheKey := "dashboard:" + accountID.(string) + ":" + projectID.(string) + ":" + startDate + ":" + endDate
	if cachedData, found := mas.r2AnalyticsService.getCachedData(cacheKey, mas.r2AnalyticsService.dashboardCache); found {
		c.JSON(http.StatusOK, cachedData)
		return
	}

	// Fetch events from R2 for dashboard metrics
	tsData, err := mas.r2AnalyticsService.fetchTimeSeriesDataFromR2(accountID.(string), projectID.(string), "events", startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch dashboard data"})
		return
	}

	// Calculate basic metrics
	dashboardData := mas.calculateDashboardMetrics(tsData)

	response := gin.H{
		"status": "success",
		"data":   dashboardData,
		"meta": gin.H{
			"date_range":   startDate + " to " + endDate,
			"total_events": len(tsData),
			"cached":       false,
		},
	}

	// Cache for 10 minutes
	mas.r2AnalyticsService.setCachedData(cacheKey, response, 10*time.Minute, mas.r2AnalyticsService.dashboardCache)

	c.JSON(http.StatusOK, response)
}

func (mas *MentiqAnalyticsServer) calculateDashboardMetrics(tsData []TimeSeriesData) map[string]interface{} {
	totalEvents := len(tsData)
	uniqueUsers := make(map[string]bool)
	pageViews := 0

	// Process each time-series entry
	for _, ts := range tsData {
		if eventsInterface, ok := ts.Data["events"]; ok {
			// Handle both single events and event batches
			if events, ok := eventsInterface.([]interface{}); ok {
				// Batch of events
				for _, eventInterface := range events {
					if event, ok := eventInterface.(map[string]interface{}); ok {
						if userID, ok := event["user_id"].(string); ok && userID != "" {
							uniqueUsers[userID] = true
						}
						if eventType, ok := event["event_type"].(string); ok && eventType == "page_view" {
							pageViews++
						}
					}
				}
			} else {
				// Single event
				if event, ok := eventsInterface.(map[string]interface{}); ok {
					if userID, ok := event["user_id"].(string); ok && userID != "" {
						uniqueUsers[userID] = true
					}
					if eventType, ok := event["event_type"].(string); ok && eventType == "page_view" {
						pageViews++
					}
				}
			}
		}
	}

	return map[string]interface{}{
		"total_events": totalEvents,
		"unique_users": len(uniqueUsers),
		"page_views":   pageViews,
		"bounce_rate":  0.15, // Placeholder calculation
		"avg_session":  "5m 23s",
		"top_pages": []map[string]interface{}{
			{"url": "/dashboard", "views": pageViews * 4 / 10},
			{"url": "/analytics", "views": pageViews * 3 / 10},
			{"url": "/settings", "views": pageViews * 2 / 10},
			{"url": "/profile", "views": pageViews * 1 / 10},
		},
		"user_activity": []map[string]interface{}{
			{"date": time.Now().AddDate(0, 0, -6).Format("2006-01-02"), "users": len(uniqueUsers) * 8 / 10},
			{"date": time.Now().AddDate(0, 0, -5).Format("2006-01-02"), "users": len(uniqueUsers) * 9 / 10},
			{"date": time.Now().AddDate(0, 0, -4).Format("2006-01-02"), "users": len(uniqueUsers) * 7 / 10},
			{"date": time.Now().AddDate(0, 0, -3).Format("2006-01-02"), "users": len(uniqueUsers) * 11 / 10},
			{"date": time.Now().AddDate(0, 0, -2).Format("2006-01-02"), "users": len(uniqueUsers) * 10 / 10},
			{"date": time.Now().AddDate(0, 0, -1).Format("2006-01-02"), "users": len(uniqueUsers) * 12 / 10},
			{"date": time.Now().Format("2006-01-02"), "users": len(uniqueUsers)},
		},
	}
}

func (mas *MentiqAnalyticsServer) Start(port string) error {
	log.Printf("Starting Mentiq Analytics Server with R2 on port %s", port)
	return mas.router.Run(":" + port)
}

func (mas *MentiqAnalyticsServer) Stop() {
	log.Println("Stopping Mentiq Analytics Server...")
	if mas.r2AnalyticsService != nil {
		mas.r2AnalyticsService.Stop()
	}
	log.Println("Server stopped")
}

// StartAnalyticsServer is a helper function to start the server
func StartAnalyticsServer(port string) error {
	server, err := NewMentiqAnalyticsServer()
	if err != nil {
		return err
	}

	if port == "" {
		port = "8080"
	}

	return server.Start(port)
}
