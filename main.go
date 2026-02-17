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

// CachedResponse represents a cached HTTP response
type CachedResponse struct {
	StatusCode int
	Body       []byte
	Headers    map[string]string
}

// Cache size limits
const (
	MaxEventsCacheSize    = 500  // event query results (can be large per entry)
	MaxDashboardCacheSize = 200  // dashboard snapshots
	MaxMetricsCacheSize   = 1000 // individual metric values
	MaxEntityCacheSize    = 500  // projects, accounts, api keys each
	MaxResponseCacheSize  = 1000 // cached HTTP responses
)

type AnalyticsService struct {
	db           *gorm.DB
	stripeClient *client.API
	stopChan     chan struct{}
	eventQueue   *EventQueue

	// Bounded data caches with TTL + LRU eviction
	eventsCache    *BoundedCache[interface{}]
	dashboardCache *BoundedCache[interface{}]
	metricsCache   *BoundedCache[interface{}]
}

func NewAnalyticsService(db *gorm.DB) (*AnalyticsService, error) {
	service := &AnalyticsService{
		db:             db,
		stripeClient:   NewStripeClient(),
		eventsCache:    NewBoundedCache[interface{}](MaxEventsCacheSize),
		dashboardCache: NewBoundedCache[interface{}](MaxDashboardCacheSize),
		metricsCache:   NewBoundedCache[interface{}](MaxMetricsCacheSize),
		stopChan:       make(chan struct{}),
		eventQueue:     NewEventQueue(db, DefaultEventQueueConfig()),
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
	emailService             *EmailService
	llmService               *LLMService
	playbookExecutor         *PlaybookExecutor
	triggerEvaluator         *TriggerEvaluator
	autoUpgradeService       *AutoUpgradeService
	integrationsService      *IntegrationsService
	automationService        *AutomationService
	automationExecutor       *AutomationExecutor

	// Bounded entity caches with TTL + LRU eviction
	projectCache     *BoundedCache[*Project]
	projectListCache *BoundedCache[[]Project] // account_projects:accountID -> []Project
	accountCache     *BoundedCache[*Account]
	apiKeyCache      *BoundedCache[*APIKey]
	recordingCache   *BoundedCache[*SessionRecording]

	// Graceful shutdown
	stopChan chan struct{}

	// Bounded response cache for analytics endpoints
	responseCache *BoundedCache[*CachedResponse]

	// Rate limiters
	globalLimiter *RateLimiter // general API rate limit
	authLimiter   *RateLimiter // login/signup rate limit (stricter)
	eventLimiter  *RateLimiter // event ingestion rate limit (higher)
}

func NewServer(db *gorm.DB, analyticsService *AnalyticsService) *Server {
	stripeService := NewStripeService(db)
	enhancedAnalyticsService := NewEnhancedAnalyticsService(db)
	emailService := NewEmailService()

	// Initialize session storage (optional - falls back to DB if not configured)
	sessionStorage, err := NewSessionStorageService()
	if err != nil {
		log.Printf("Session storage not configured: %v. Using database storage for recordings.", err)
		sessionStorage = nil
	}

	// Initialize Mailchimp service
	mailchimpService := NewMailchimpService(db)

	// Initialize LLM service for automation
	llmService := NewLLMService(db)

	// Initialize automation service
	automationService := NewAutomationService(db, llmService, mailchimpService)

	// Initialize playbook executor and trigger evaluator
	playbookExecutor := NewPlaybookExecutor(db, emailService, mailchimpService)
	triggerEvaluator := NewTriggerEvaluator(db)

	// Initialize automation executor
	automationExecutor := NewAutomationExecutor(db, automationService, mailchimpService)

	server := &Server{
		db:                       db,
		analyticsService:         analyticsService,
		stripeService:            stripeService,
		enhancedAnalyticsService: enhancedAnalyticsService,
		sessionStorage:           sessionStorage,
		emailService:             emailService,
		playbookExecutor:         playbookExecutor,
		triggerEvaluator:         triggerEvaluator,
		autoUpgradeService:       NewAutoUpgradeService(db),
		integrationsService:      NewIntegrationsService(db),
		automationService:        automationService,
		automationExecutor:       automationExecutor,
		projectCache:             NewBoundedCache[*Project](MaxEntityCacheSize),
		projectListCache:         NewBoundedCache[[]Project](200),
		accountCache:             NewBoundedCache[*Account](MaxEntityCacheSize),
		apiKeyCache:              NewBoundedCache[*APIKey](MaxEntityCacheSize),
		recordingCache:           NewBoundedCache[*SessionRecording](MaxEntityCacheSize),
		stopChan:                 make(chan struct{}),
		responseCache:            NewBoundedCache[*CachedResponse](MaxResponseCacheSize),
		globalLimiter:            NewRateLimiter(100, 1*time.Minute),
		authLimiter:              NewRateLimiter(10, 1*time.Minute),
		eventLimiter:             NewRateLimiter(5000, 1*time.Minute),
	}

	// Start background workers
	playbookExecutor.Start()
	triggerEvaluator.Start()
	automationExecutor.Start()

	// Start entity cache cleanup (every 15 minutes)
	go server.startEntityCacheCleanup()

	return server
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

// Shutdown gracefully stops all background workers
func (s *Server) Shutdown() {
	log.Println("Shutting down server background workers...")

	// Stop background workers
	if s.playbookExecutor != nil {
		log.Println("Stopping playbook executor...")
		s.playbookExecutor.Stop()
	}

	if s.triggerEvaluator != nil {
		log.Println("Stopping trigger evaluator...")
		s.triggerEvaluator.Stop()
	}

	if s.automationExecutor != nil {
		log.Println("Stopping automation executor...")
		s.automationExecutor.Stop()
	}

	// Stop rate limiters
	if s.globalLimiter != nil {
		s.globalLimiter.Stop()
	}
	if s.authLimiter != nil {
		s.authLimiter.Stop()
	}
	if s.eventLimiter != nil {
		s.eventLimiter.Stop()
	}

	// Signal entity cache cleanup to stop
	select {
	case <-s.stopChan:
		// Already closed
	default:
		close(s.stopChan)
	}

	log.Println("All server background workers stopped")
}

// getUserRole returns the role of a user within an account
// It first checks the User table, then falls back to Account table for legacy account owners
func (s *Server) getUserRole(accountID, email string) (string, error) {
	// First, try to find the user in the User table
	var user User
	if err := s.db.Where("account_id = ? AND email = ?", accountID, email).First(&user).Error; err == nil {
		return user.Role, nil
	}

	// If not found in User table, check if this is an account owner
	var account Account
	if err := s.db.Where("id = ? AND email = ?", accountID, email).First(&account).Error; err == nil {
		// Account owner always has "owner" role
		return "owner", nil
	}

	return "", fmt.Errorf("user not found in account")
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

type UpdateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
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
	IsAdmin   bool   `json:"is_admin"`
	Role      string `json:"role,omitempty"` // "owner", "admin", "member", "viewer"
	ProjectID string `json:"project_id,omitempty"`
	APIKeyID  string `json:"api_key_id,omitempty"`
	Type      string `json:"type"` // "access" or "refresh"
	Exp       int64  `json:"exp"`  // Expiration time
	Iat       int64  `json:"iat"`  // Issued at time
}

// GenerateJWT generates a JWT token for an account with HMAC-SHA256 signing
func GenerateJWT(accountID, email, tokenType string, isAdmin bool, role string, expiresInHours int) (string, error) {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return "", fmt.Errorf("JWT_SECRET must be configured")
	}

	now := time.Now().Unix()
	expiryTime := time.Now().Add(time.Duration(expiresInHours) * time.Hour).Unix()

	claims := JWTClaims{
		AccountID: accountID,
		Email:     email,
		IsAdmin:   isAdmin,
		Role:      role,
		Type:      tokenType,
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

// Entity cache helper methods

func (s *Server) startEntityCacheCleanup() {
	cleanupTicker := time.NewTicker(15 * time.Minute)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-cleanupTicker.C:
			cleaned := s.projectCache.CleanExpired()
			cleaned += s.projectListCache.CleanExpired()
			cleaned += s.accountCache.CleanExpired()
			cleaned += s.apiKeyCache.CleanExpired()
			cleaned += s.recordingCache.CleanExpired()
			if cleaned > 0 {
				log.Printf("Entity cache cleanup: removed %d expired entries", cleaned)
			}
		case <-s.stopChan:
			log.Println("Entity cache cleanup worker stopped")
			return
		}
	}
}

// Cached entity retrieval methods

func (s *Server) getCachedProject(projectID string) (*Project, bool) {
	return s.projectCache.Get(projectID)
}

func (s *Server) setCachedProject(project *Project) {
	s.projectCache.Set(project.ID, project, 30*time.Minute)
}

func (s *Server) invalidateProjectCache(projectID string) {
	s.projectCache.Delete(projectID)
}

func (s *Server) getCachedAccount(accountID string) (*Account, bool) {
	return s.accountCache.Get(accountID)
}

func (s *Server) setCachedAccount(account *Account) {
	s.accountCache.Set(account.ID, account, 30*time.Minute)
}

func (s *Server) getCachedAPIKey(token string) (*APIKey, bool) {
	return s.apiKeyCache.Get(token)
}

func (s *Server) setCachedAPIKey(apiKey *APIKey) {
	s.apiKeyCache.Set(apiKey.Key, apiKey, 30*time.Minute)
}

func (s *Server) invalidateAPIKeyCache(token string) {
	s.apiKeyCache.Delete(token)
}

func (s *Server) getCachedRecording(recordingID string) (*SessionRecording, bool) {
	return s.recordingCache.Get(recordingID)
}

func (s *Server) setCachedRecording(recording *SessionRecording) {
	s.recordingCache.Set(recording.ID, recording, 15*time.Minute)
}

// ========================================
// Response Caching Middleware (Aggressive)
// ========================================

// responseRecorder wraps gin.ResponseWriter to capture response body
type responseRecorder struct {
	gin.ResponseWriter
	body       []byte
	statusCode int
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	r.body = append(r.body, data...)
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// generateResponseCacheKey creates a unique key for caching responses
func (s *Server) generateResponseCacheKey(c *gin.Context) string {
	// Include path, query params, account ID, and project ID for uniqueness
	accountID, _ := c.Get("account_id")
	projectID := c.Param("project_id")
	queryString := c.Request.URL.RawQuery

	key := fmt.Sprintf("resp:%s:%s:%v:%s", c.Request.URL.Path, projectID, accountID, queryString)
	return key
}

// getCachedResponse retrieves a cached response if valid
func (s *Server) getCachedResponse(cacheKey string) (*CachedResponse, bool) {
	return s.responseCache.Get(cacheKey)
}

// setCachedResponse stores a response in the cache
func (s *Server) setCachedResponse(cacheKey string, statusCode int, body []byte, headers map[string]string, ttl time.Duration) {
	s.responseCache.Set(cacheKey, &CachedResponse{
		StatusCode: statusCode,
		Body:       body,
		Headers:    headers,
	}, ttl)
}

// startResponseCacheCleanup periodically cleans up expired response cache entries
func (s *Server) startResponseCacheCleanup() {
	cleanupTicker := time.NewTicker(10 * time.Minute)
	defer cleanupTicker.Stop()

	for range cleanupTicker.C {
		cleaned := s.responseCache.CleanExpired()
		if cleaned > 0 {
			log.Printf("Response cache cleanup: removed %d expired entries", cleaned)
		}
	}
}

// cacheResponseMiddleware creates a middleware that caches GET responses with specified TTL
// This is an "excessive" caching strategy for analytics endpoints
func (s *Server) cacheResponseMiddleware(ttl time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only cache GET requests
		if c.Request.Method != http.MethodGet {
			c.Next()
			return
		}

		// Generate cache key
		cacheKey := s.generateResponseCacheKey(c)

		// Check if we have a cached response
		if cached, found := s.getCachedResponse(cacheKey); found {
			// Add cache hit header for debugging
			c.Header("X-Cache", "HIT")

			// Set any cached headers
			for key, value := range cached.Headers {
				c.Header(key, value)
			}

			c.Data(cached.StatusCode, "application/json; charset=utf-8", cached.Body)
			c.Abort()
			return
		}

		// Cache miss - record the response
		recorder := &responseRecorder{
			ResponseWriter: c.Writer,
			body:           []byte{},
			statusCode:     http.StatusOK,
		}
		c.Writer = recorder

		// Add cache miss header
		c.Header("X-Cache", "MISS")

		// Process the request
		c.Next()

		// Only cache successful responses (2xx)
		if recorder.statusCode >= 200 && recorder.statusCode < 300 {
			headers := make(map[string]string)
			headers["Content-Type"] = "application/json; charset=utf-8"

			s.setCachedResponse(cacheKey, recorder.statusCode, recorder.body, headers, ttl)
		}
	}
}

// Cache TTL constants for different analytics types (AGGRESSIVE/EXCESSIVE caching)
const (
	CacheTTLStripeMetrics      = 30 * time.Minute // Stripe data doesn't change frequently
	CacheTTLStripeAnalytics    = 45 * time.Minute // Revenue analytics - stable data
	CacheTTLLocationAnalytics  = 1 * time.Hour    // Geographic data is very stable
	CacheTTLDeviceAnalytics    = 1 * time.Hour    // Device/browser stats are stable
	CacheTTLCohortAnalytics    = 45 * time.Minute // Cohort/retention data
	CacheTTLFeatureAdoption    = 30 * time.Minute // Feature usage analytics
	CacheTTLPaidUserMetrics    = 30 * time.Minute // Subscription user metrics
	CacheTTLChurnMetrics       = 30 * time.Minute // Churn analytics
	CacheTTLSubscriptionHealth = 30 * time.Minute // Subscription health overview
	CacheTTLFunnelAnalytics    = 25 * time.Minute // Conversion funnels
	CacheTTLSessionAnalytics   = 20 * time.Minute // Session-based analytics
	CacheTTLExperimentResults  = 15 * time.Minute // A/B test results (more dynamic)
	CacheTTLHeatmapData        = 45 * time.Minute // Heatmap aggregations
	CacheTTLRecordingsList     = 15 * time.Minute // Recording lists
	CacheTTLSessionsList       = 15 * time.Minute // Sessions list
	CacheTTLFeatureUsage       = 30 * time.Minute // Feature usage stats
	CacheTTLOnboardingStats    = 30 * time.Minute // Onboarding statistics
)

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

	// Initialize webhook secret
	InitWebhookSecret()

	// Start response cache cleanup (every 10 minutes)
	go server.startResponseCacheCleanup()

	// Setup Gin router
	router := gin.Default()

	// Setup CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	config.AllowCredentials = true
	router.Use(cors.New(config))

	// Public routes (auth routes have stricter IP-based rate limiting)
	authRL := IPRateLimitMiddleware(server.authLimiter)
	router.POST("/signup", authRL, server.signupHandler)
	router.POST("/login", authRL, server.loginHandler)
	router.POST("/refresh", server.refreshTokenHandler)
	router.POST("/api/v1/invitations/accept", server.acceptInvitationHandler)
	router.GET("/verify-email", server.verifyEmailHandler)
	router.POST("/resend-verification", server.resendVerificationHandler)
	router.POST("/api/auth/google", authRL, server.googleAuthHandler)
	router.POST("/forgot-password", authRL, server.forgotPasswordHandler)
	router.POST("/reset-password", authRL, server.resetPasswordHandler)
	router.GET("/health", healthCheckHandler)

	// Waitlist route (public, no auth required)
	router.POST("/api/v1/waitlist", server.joinWaitlistHandler)
	router.GET("/api/v1/unsubscribe", server.unsubscribeHandler)

	// Webhook routes (no authentication - secured by signature validation)
	webhookRoutes := router.Group("/webhook")
	{
		webhookRoutes.POST("/subscription", server.webhookSubscriptionHandler)
		webhookRoutes.POST("/payment", server.webhookPaymentHandler)
	}

	// OAuth callback routes (no authentication - OAuth flow handles security)
	router.GET("/api/v1/integrations/mailchimp/callback", server.integrationsService.MailchimpCallbackHandler)

	// Serve static dashboard
	router.Static("/static", "./")
	router.GET("/", func(c *gin.Context) {
		c.File("./dashboard.html")
	})

	// API routes
	apiV1 := router.Group("/api/v1")

	apiV1.Use(server.authMiddleware) // Apply auth middleware to all v1 routes
	apiV1.Use(AccountRateLimitMiddleware(server.globalLimiter))
	{
		// User endpoints
		apiV1.GET("/me", server.getMeHandler)

		// Event ingestion routes use a higher rate limit
		eventRL := AccountRateLimitMiddleware(server.eventLimiter)
		apiV1.POST("/events", eventRL, server.analyticsService.ingestEventHandler)
		apiV1.POST("/events/batch", eventRL, server.analyticsService.batchIngestHandler)
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

		// Onboarding routes
		apiV1.GET("/onboarding/status", server.getOnboardingStatusHandler)
		apiV1.PUT("/onboarding/status", server.updateOnboardingStatusHandler)
		apiV1.POST("/onboarding/tasks/:task/complete", server.markTaskCompleteHandler)
		apiV1.GET("/onboarding/tasks", server.getOnboardingTasksHandler)

		// Account settings routes
		apiV1.GET("/account/profile", server.getAccountProfileHandler)
		apiV1.PUT("/account/profile", server.updateAccountProfileHandler)
		apiV1.POST("/account/change-password", server.changePasswordHandler)

		// Project and API Key management
		apiV1.POST("/projects", server.createProjectHandler)
		apiV1.GET("/projects", server.listProjectsHandler)
		apiV1.PUT("/projects/:project_id", server.updateProjectHandler)
		apiV1.DELETE("/projects/:project_id", server.deleteProjectHandler)
		apiV1.GET("/projects/:project_id/events", server.analyticsService.GetEventsHandler)
		apiV1.POST("/projects/:project_id/apikeys", server.createApiKeyHandler)
		apiV1.GET("/projects/:project_id/apikeys", server.listApiKeysHandler)
		apiV1.PUT("/projects/:project_id/apikeys/:key_id", server.updateApiKeyHandler)
		apiV1.DELETE("/projects/:project_id/apikeys/:key_id", server.deleteApiKeyHandler)
		apiV1.PUT("/projects/:project_id/stripe-key", server.updateProjectStripeApiKeyHandler)

		// Project Member management
		apiV1.POST("/projects/:project_id/members", server.addProjectMemberHandler)
		apiV1.GET("/projects/:project_id/members", server.listProjectMembersHandler)
		apiV1.PUT("/projects/:project_id/members/:member_id", server.updateProjectMemberHandler)
		apiV1.DELETE("/projects/:project_id/members/:member_id", server.removeProjectMemberHandler)
		apiV1.GET("/users/:user_id/projects", server.getUserProjectsHandler)

		// ========================================
		// Stripe Revenue Analytics routes (CACHED)
		// ========================================
		apiV1.POST("/projects/:project_id/stripe/sync", server.stripeService.SyncStripeDataHandler)
		apiV1.GET("/projects/:project_id/stripe/metrics", server.cacheResponseMiddleware(CacheTTLStripeMetrics), server.stripeService.GetRevenueMetricsHandler)
		apiV1.GET("/projects/:project_id/stripe/analytics", server.cacheResponseMiddleware(CacheTTLStripeAnalytics), server.stripeService.GetRevenueAnalyticsHandler)
		apiV1.GET("/projects/:project_id/stripe/customers", server.cacheResponseMiddleware(CacheTTLStripeAnalytics), server.stripeService.GetCustomerAnalyticsHandler)

		// ========================================
		// Session Recording routes (CACHED)
		// ========================================
		apiV1.POST("/sessions/:session_id/recordings", server.ingestRecordingHandler)
		apiV1.GET("/projects/:project_id/recordings", server.cacheResponseMiddleware(CacheTTLRecordingsList), server.listRecordingsHandler)
		apiV1.GET("/projects/:project_id/recordings/:recordingId", server.getRecordingHandler) // Individual recordings not cached (large data)
		apiV1.GET("/projects/:project_id/sessions", server.cacheResponseMiddleware(CacheTTLSessionsList), server.getSessionsHandler)
		apiV1.GET("/projects/:project_id/sessions/:sessionId", server.getSessionHandler) // Individual sessions not cached

		// ========================================
		// Heatmap routes (CACHED)
		// ========================================
		apiV1.GET("/projects/:project_id/heatmaps", server.cacheResponseMiddleware(CacheTTLHeatmapData), server.analyticsService.GetHeatmapHandler)
		apiV1.GET("/projects/:project_id/heatmaps/pages", server.cacheResponseMiddleware(CacheTTLHeatmapData), server.getHeatmapPagesHandler)

		// ========================================
		// Enhanced Analytics routes (CACHED - 45min to 1hr)
		// ========================================
		apiV1.GET("/projects/:project_id/analytics/location", server.cacheResponseMiddleware(CacheTTLLocationAnalytics), server.enhancedAnalyticsService.LocationAnalyticsHandler)
		apiV1.GET("/projects/:project_id/analytics/devices", server.cacheResponseMiddleware(CacheTTLDeviceAnalytics), server.enhancedAnalyticsService.DeviceAnalyticsHandler)
		apiV1.GET("/projects/:project_id/analytics/cohorts", server.cacheResponseMiddleware(CacheTTLCohortAnalytics), server.enhancedAnalyticsService.RetentionCohortHandler)
		apiV1.GET("/projects/:project_id/analytics/features", server.cacheResponseMiddleware(CacheTTLFeatureAdoption), server.enhancedAnalyticsService.FeatureAdoptionHandler)

		// ========================================
		// Subscription Analytics routes (CACHED - 30min)
		// ========================================
		apiV1.GET("/projects/:project_id/analytics/paid-users", server.cacheResponseMiddleware(CacheTTLPaidUserMetrics), server.analyticsService.GetPaidUsersMetricsHandler)
		apiV1.GET("/projects/:project_id/analytics/churn-metrics", server.cacheResponseMiddleware(CacheTTLChurnMetrics), server.analyticsService.GetChurnMetricsHandler)
		apiV1.GET("/projects/:project_id/analytics/subscription-health", server.cacheResponseMiddleware(CacheTTLSubscriptionHealth), server.analyticsService.GetSubscriptionHealthHandler)
		apiV1.GET("/projects/:project_id/analytics/churn", server.cacheResponseMiddleware(CacheTTLChurnMetrics), server.enhancedAnalyticsService.ChurnRiskHandler)
		apiV1.GET("/projects/:project_id/analytics/churn/export", server.enhancedAnalyticsService.ExportChurnRiskHandler) // Export endpoint - not cached
		apiV1.GET("/projects/:project_id/analytics/funnels", server.cacheResponseMiddleware(CacheTTLFunnelAnalytics), server.enhancedAnalyticsService.ConversionFunnelHandler)
		apiV1.GET("/projects/:project_id/analytics/sessions", server.cacheResponseMiddleware(CacheTTLSessionAnalytics), server.enhancedAnalyticsService.SessionAnalyticsHandler)

		// ========================================
		// A/B Testing routes (CACHED for results only)
		// ========================================
		apiV1.POST("/projects/:project_id/experiments", server.CreateExperiment)
		apiV1.GET("/projects/:project_id/experiments", server.GetExperiments)
		apiV1.GET("/projects/:project_id/experiments/:experimentId", server.GetExperiment)
		apiV1.PATCH("/projects/:project_id/experiments/:experimentId", server.UpdateExperiment)
		apiV1.DELETE("/projects/:project_id/experiments/:experimentId", server.DeleteExperiment)
		apiV1.GET("/projects/:project_id/experiments/:experimentId/results", server.cacheResponseMiddleware(CacheTTLExperimentResults), server.GetExperimentResults)

		// Global experiment routes (for SDK) - NO CACHING (real-time assignment needed)
		apiV1.POST("/experiments/:experimentKey/assignment", server.GetAssignment)
		apiV1.POST("/experiments/track", server.TrackConversion)
		apiV1.PUT("/experiments/:id/status", server.UpdateExperimentStatus)

		// Playbook routes
		apiV1.POST("/projects/:project_id/playbooks", server.createPlaybookHandler)
		apiV1.GET("/projects/:project_id/playbooks", server.getPlaybooksHandler)
		apiV1.GET("/projects/:project_id/playbooks/summary", server.getPlaybooksSummaryHandler)
		apiV1.GET("/projects/:project_id/playbooks/:playbook_id", server.getPlaybookHandler)
		apiV1.PUT("/projects/:project_id/playbooks/:playbook_id", server.updatePlaybookHandler)
		apiV1.DELETE("/projects/:project_id/playbooks/:playbook_id", server.deletePlaybookHandler)
		apiV1.PATCH("/projects/:project_id/playbooks/:playbook_id/status", server.updatePlaybookStatusHandler)

		// Playbook Steps routes
		apiV1.POST("/projects/:project_id/playbooks/:playbook_id/steps", server.addStepHandler)
		apiV1.PUT("/projects/:project_id/playbooks/:playbook_id/steps/:step_id", server.updateStepHandler)
		apiV1.DELETE("/projects/:project_id/playbooks/:playbook_id/steps/:step_id", server.deleteStepHandler)
		apiV1.PUT("/projects/:project_id/playbooks/:playbook_id/steps/reorder", server.reorderStepsHandler)

		// Playbook Triggers routes
		apiV1.POST("/projects/:project_id/playbooks/:playbook_id/triggers", server.createTriggerHandler)
		apiV1.GET("/projects/:project_id/playbooks/:playbook_id/triggers", server.getTriggersHandler)
		apiV1.PUT("/projects/:project_id/playbooks/:playbook_id/triggers/:trigger_id", server.updateTriggerHandler)
		apiV1.DELETE("/projects/:project_id/playbooks/:playbook_id/triggers/:trigger_id", server.deleteTriggerHandler)
		apiV1.PATCH("/projects/:project_id/playbooks/:playbook_id/triggers/:trigger_id/toggle", server.toggleTriggerHandler)

		// Playbook Enrollments routes
		apiV1.POST("/projects/:project_id/playbooks/:playbook_id/enroll", server.enrollUsersHandler)
		apiV1.GET("/projects/:project_id/playbooks/:playbook_id/enrollments", server.getEnrollmentsHandler)
		apiV1.POST("/projects/:project_id/playbooks/:playbook_id/enrollments/:enrollment_id/exit", server.exitEnrollmentHandler)

		// Playbook Analytics routes
		apiV1.GET("/projects/:project_id/playbooks/:playbook_id/analytics", server.getPlaybookAnalyticsHandler)

		// Playbook LLM Generation routes
		apiV1.POST("/projects/:project_id/playbooks/generate", server.generatePlaybookHandler)
		apiV1.GET("/projects/:project_id/playbooks/generate/:generation_id", server.getGenerationStatusHandler)
		apiV1.POST("/projects/:project_id/playbooks/generate/:generation_id/apply", server.applyGeneratedPlaybookHandler)

		// Mentiq Subscription Management routes - NO CACHING (mutation heavy)
		apiV1.POST("/subscriptions", server.createOrUpdateSubscriptionHandler)
		apiV1.GET("/subscriptions/:account_id", server.getSubscriptionHandler)
		apiV1.POST("/payments", server.createPaymentHandler)
		apiV1.GET("/payments/:account_id", server.listPaymentsHandler)

		// Auto-upgrade routes
		apiV1.POST("/subscriptions/check-upgrades", server.autoUpgradeService.CheckUpgradesHandler)
		apiV1.POST("/subscriptions/:account_id/check-upgrade", server.autoUpgradeService.CheckSingleAccountUpgradeHandler)

		// ========================================
		// Feature Tracking & Onboarding routes (CACHED)
		// ========================================
		apiV1.GET("/projects/:project_id/features/usage", server.cacheResponseMiddleware(CacheTTLFeatureUsage), server.getFeatureUsageHandler)
		apiV1.GET("/projects/:project_id/onboarding/stats", server.cacheResponseMiddleware(CacheTTLOnboardingStats), server.getOnboardingStatsHandler)
		apiV1.GET("/projects/:project_id/users/:user_id/journey", server.getUserFeatureJourneyHandler) // User-specific, not cached

		// Team Invitation routes
		apiV1.POST("/invitations", server.createInvitationHandler)
		apiV1.GET("/invitations", server.listInvitationsHandler)
		apiV1.DELETE("/invitations/:id", server.cancelInvitationHandler)
		apiV1.POST("/invitations/:id/resend", server.resendInvitationHandler)

		// Team Member management routes
		apiV1.GET("/team/members", server.listAccountMembersHandler)
		apiV1.PUT("/team/members/:id", server.updateAccountMemberHandler)
		apiV1.DELETE("/team/members/:id", server.removeAccountMemberHandler)

		// Support Ticket routes
		apiV1.POST("/tickets", CreateTicketHandler(server.db))
		apiV1.GET("/tickets", GetTicketsHandler(server.db))
		apiV1.GET("/tickets/:id", GetTicketHandler(server.db))
		apiV1.PUT("/tickets/:id", UpdateTicketHandler(server.db))
		apiV1.POST("/tickets/:id/comments", AddCommentHandler(server.db))

		// ========================================
		// Integration routes (Mailchimp, etc.)
		// ========================================
		apiV1.GET("/projects/:project_id/integrations", server.integrationsService.GetIntegrationsHandler)
		apiV1.GET("/projects/:project_id/integrations/:provider", server.integrationsService.GetIntegrationHandler)

		// Mailchimp-specific routes
		apiV1.POST("/projects/:project_id/integrations/mailchimp/connect", server.integrationsService.ConnectMailchimpHandler)
		apiV1.POST("/projects/:project_id/integrations/mailchimp/callback", server.integrationsService.MailchimpCallbackAPIHandler)
		apiV1.DELETE("/projects/:project_id/integrations/mailchimp", server.integrationsService.DisconnectMailchimpHandler)
		apiV1.GET("/projects/:project_id/integrations/mailchimp/audiences", server.integrationsService.GetMailchimpAudiencesHandler)
		apiV1.PUT("/projects/:project_id/integrations/mailchimp/settings", server.integrationsService.UpdateMailchimpSettingsHandler)
		apiV1.POST("/projects/:project_id/integrations/mailchimp/sync", server.integrationsService.TriggerMailchimpSyncHandler)
		apiV1.GET("/projects/:project_id/integrations/mailchimp/logs", server.integrationsService.GetMailchimpSyncLogsHandler)

		// ========================================
		// Automation routes
		// ========================================
		// Automation settings
		apiV1.POST("/projects/:project_id/automations", server.createAutomationHandler)
		apiV1.GET("/projects/:project_id/automations", server.getAutomationsHandler)
		apiV1.GET("/projects/:project_id/automations/:automation_id", server.getAutomationHandler)
		apiV1.PUT("/projects/:project_id/automations/:automation_id", server.updateAutomationHandler)
		apiV1.DELETE("/projects/:project_id/automations/:automation_id", server.deleteAutomationHandler)

		// Email templates
		apiV1.POST("/projects/:project_id/email-templates", server.createEmailTemplateHandler)
		apiV1.GET("/projects/:project_id/email-templates", server.getEmailTemplatesHandler)
		apiV1.PUT("/projects/:project_id/email-templates/:template_id", server.updateEmailTemplateHandler)
		apiV1.DELETE("/projects/:project_id/email-templates/:template_id", server.deleteEmailTemplateHandler)

		// Discount codes
		apiV1.POST("/projects/:project_id/discount-codes", server.createDiscountCodeHandler)
		apiV1.GET("/projects/:project_id/discount-codes", server.getDiscountCodesHandler)
		apiV1.PUT("/projects/:project_id/discount-codes/:code_id", server.updateDiscountCodeHandler)

		// Automation executions (read-only for now - created by automation engine)
		apiV1.GET("/projects/:project_id/automation-executions", server.getAutomationExecutionsHandler)

		// Automation testing/triggering
		apiV1.POST("/projects/:project_id/automations/:automation_id/trigger", server.triggerAutomationHandler)
		apiV1.POST("/projects/:project_id/automations/:automation_id/test", server.testAutomationHandler)
	}

	// Test/Debug routes - No authentication (disable in production!)
	testAPI := router.Group("/api/v1/test")
	{
		testAPI.GET("/recordings", server.testListAllRecordingsHandler)
		testAPI.GET("/recordings/:session_id", server.testGetRecordingBySessionHandler)
	}

	// Admin routes - require admin authentication
	adminAPI := router.Group("/api/v1/admin")
	adminAPI.Use(server.authMiddleware)
	adminAPI.Use(server.adminMiddleware)
	{
		adminAPI.GET("/accounts", server.adminListAccountsHandler)
		adminAPI.GET("/accounts/:account_id", server.adminGetAccountHandler)
		adminAPI.GET("/accounts/:account_id/projects", server.adminListAccountProjectsHandler)
		adminAPI.GET("/accounts/:account_id/users", server.adminListAccountUsersHandler)
		adminAPI.GET("/projects", server.adminListAllProjectsHandler)
		adminAPI.GET("/events", server.adminListAllEventsHandler)
		adminAPI.GET("/users/:user_id/data", server.adminGetUserDataHandler)
		adminAPI.GET("/users/:user_id/projects", server.adminGetUserProjectsHandler)
		adminAPI.GET("/users-with-projects", server.adminGetAllUsersWithProjectsHandler)
		adminAPI.GET("/projects/:project_id/data", server.adminGetProjectDataHandler)
		adminAPI.PUT("/accounts/:account_id/admin", server.adminToggleAdminHandler)

		// Admin Support Ticket routes
		adminAPI.GET("/tickets", GetAllTicketsHandler(server.db))
		adminAPI.GET("/tickets/stats", GetTicketStatsHandler(server.db))

		// Admin Test User creation route
		adminAPI.POST("/test-users", server.adminCreateTestUserHandler)

		// Admin Waitlist routes
		adminAPI.GET("/waitlist", server.getWaitlistHandler)
		adminAPI.POST("/waitlist/:id/grant-access", server.grantWaitlistAccessHandler)
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
	log.Println("Received shutdown signal, initiating graceful shutdown...")

	// Create a deadline for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop accepting new connections and wait for active requests to complete
	log.Println("Stopping HTTP server...")
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Gracefully stop all server background workers
	server.Shutdown()

	// Gracefully stop the analytics service (flush cache)
	log.Println("Stopping analytics service...")
	analyticsService.Stop()

	log.Println("Graceful shutdown completed")
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
func (as *AnalyticsService) getCachedData(cacheKey string, cacheMap *BoundedCache[interface{}]) (interface{}, bool) {
	return cacheMap.Get(cacheKey)
}

// setCachedData stores data in cache with TTL
func (as *AnalyticsService) setCachedData(cacheKey string, data interface{}, ttl time.Duration, cacheMap *BoundedCache[interface{}]) {
	cacheMap.Set(cacheKey, data, ttl)
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
	cleaned := as.eventsCache.CleanExpired()
	cleaned += as.dashboardCache.CleanExpired()
	cleaned += as.metricsCache.CleanExpired()

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

	if as.eventQueue != nil {
		as.eventQueue.Stop()
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
	return as.eventsCache.Len()
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
	eventsCount := as.eventsCache.Len()
	dashboardCount := as.dashboardCache.Len()
	metricsCount := as.metricsCache.Len()

	// Replace with fresh bounded caches
	as.eventsCache = NewBoundedCache[interface{}](MaxEventsCacheSize)
	as.dashboardCache = NewBoundedCache[interface{}](MaxDashboardCacheSize)
	as.metricsCache = NewBoundedCache[interface{}](MaxMetricsCacheSize)

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

	// SECURITY: Validate project belongs to authenticated account
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can update Stripe API key"})
		return
	}

	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized Stripe key update attempt - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	if err := s.db.Model(&Project{}).Where("id = ? AND account_id = ?", projectID, accountID.(string)).Update("stripe_api_key", req.ApiKey).Error; err != nil {
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
		Events      json.RawMessage `json:"events"`
		Duration    int             `json:"duration"`
		StartURL    string          `json:"start_url"`
		AccountID   string          `json:"account_id"`
		ProjectID   string          `json:"project_id"`
		UserID      *string         `json:"user_id"`
		IsFinal     bool            `json:"is_final"`
		EventOffset int             `json:"event_offset"`
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

	// Determine storage strategy
	useS3Storage := s.sessionStorage != nil
	if useS3Storage {
		// Upload to S3/R2 - returns base path for all chunks
		storagePath, err = s.sessionStorage.UploadRecording(
			sessionID,
			recordingData.ProjectID,
			recordingData.AccountID,
			recordingData.Events,
		)
		if err != nil {
			log.Printf("Failed to upload recording to S3: %v. Falling back to DB storage.", err)
			// Fall back to DB storage
			useS3Storage = false
			eventsJSON = recordingData.Events
			storagePath = ""
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
		// Update existing recording - APPEND events instead of replacing
		recording.ID = existingRecording.ID
		recording.CreatedAt = existingRecording.CreatedAt

		// Append new events to existing events
		if useS3Storage {
			// S3 storage - chunks are uploaded separately, just update count and keep base path
			recording.EventCount = existingRecording.EventCount + eventCount
			// Keep the same storage path (base path for all chunks)
			recording.StoragePath = existingRecording.StoragePath
			recording.RecordingData = nil // No data in DB when using S3
		} else if len(existingRecording.RecordingData) > 0 {
			// DB storage - append events
			var existingEvents []interface{}
			var newEvents []interface{}

			if err := json.Unmarshal(existingRecording.RecordingData, &existingEvents); err == nil {
				if err := json.Unmarshal(recordingData.Events, &newEvents); err == nil {
					// Combine events
					allEvents := append(existingEvents, newEvents...)
					combinedJSON, err := json.Marshal(allEvents)
					if err == nil {
						recording.RecordingData = combinedJSON
						recording.EventCount = len(allEvents)
					} else {
						log.Printf("Failed to marshal combined events: %v", err)
					}
				} else {
					log.Printf("Failed to unmarshal new events: %v", err)
				}
			} else {
				log.Printf("Failed to unmarshal existing events: %v", err)
			}
		} else {
			// First upload to DB storage
			recording.EventCount = eventCount
		}

		// Update duration to the maximum (SDK sends cumulative duration)
		if recordingData.Duration > existingRecording.Duration {
			recording.Duration = recordingData.Duration
		} else {
			recording.Duration = existingRecording.Duration
		}

		if err := s.db.Save(&recording).Error; err != nil {
			log.Printf("Failed to update recording: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save recording"})
			return
		}

		log.Printf("Appended %d events to existing recording %s (total: %d events, duration: %ds)",
			eventCount, recording.ID, recording.EventCount, recording.Duration)
	} else {
		// Create new recording
		if err := s.db.Create(&recording).Error; err != nil {
			log.Printf("Failed to create recording: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save recording"})
			return
		}
		log.Printf("Created new recording %s with %d events (duration: %ds)",
			recording.ID, recording.EventCount, recording.Duration)
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

	// SECURITY: Validate project belongs to authenticated account
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized recordings access - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
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

	// SECURITY: Validate project belongs to authenticated account
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized recording access - Account: %s, Project: %s, Recording: %s", accountID, projectID, recordingID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	// Try to get from cache first
	var recording SessionRecording
	if cachedRecording, found := s.getCachedRecording(recordingID); found && cachedRecording.ProjectID == projectID {
		recording = *cachedRecording
	} else {
		// Fetch recording metadata from database and verify it belongs to the project
		if err := s.db.Where("id = ? AND project_id = ?", recordingID, projectID).First(&recording).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
				return
			}
			log.Printf("Failed to fetch recording: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve recording"})
			return
		}
		// Cache for future requests
		s.setCachedRecording(&recording)
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
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (s *Server) getSessionsHandler(c *gin.Context) {
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

	// SECURITY: Validate project belongs to authenticated account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized sessions access - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
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

	// Date range
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	// Parse dates for SQL query
	startTime, _ := time.Parse("2006-01-02", startDate)
	endTime, _ := time.Parse("2006-01-02", endDate)
	endTime = endTime.Add(24*time.Hour - time.Nanosecond)

	// OPTIMIZED: Use SQL aggregation to get session data directly
	type SessionAggregation struct {
		SessionID  string    `gorm:"column:session_id"`
		UserID     string    `gorm:"column:user_id"`
		StartTime  time.Time `gorm:"column:start_time"`
		EndTime    time.Time `gorm:"column:end_time"`
		EventCount int64     `gorm:"column:event_count"`
		PageViews  int64     `gorm:"column:page_views"`
		Device     string    `gorm:"column:device"`
		Browser    string    `gorm:"column:browser"`
		Country    string    `gorm:"column:country"`
	}

	// Build the query
	query := s.db.Table("events").
		Select(`
			session_id,
			MAX(user_id) as user_id,
			MIN(timestamp) as start_time,
			MAX(timestamp) as end_time,
			COUNT(*) as event_count,
			SUM(CASE WHEN event_type = 'page_view' THEN 1 ELSE 0 END) as page_views,
			MAX(device) as device,
			MAX(browser) as browser,
			MAX(country) as country
		`).
		Where("project_id = ? AND account_id = ? AND timestamp BETWEEN ? AND ? AND session_id IS NOT NULL AND session_id != ''",
			projectID, accountID.(string), startTime, endTime).
		Group("session_id").
		Order("start_time DESC")

	// Apply user filter if provided
	if userID != "" {
		query = query.Having("MAX(user_id) = ?", userID)
	}

	// Get total count first
	var totalCount int64
	countQuery := s.db.Table("events").
		Select("COUNT(DISTINCT session_id)").
		Where("project_id = ? AND account_id = ? AND timestamp BETWEEN ? AND ? AND session_id IS NOT NULL AND session_id != ''",
			projectID, accountID.(string), startTime, endTime)
	if userID != "" {
		countQuery = countQuery.Where("user_id = ?", userID)
	}
	countQuery.Scan(&totalCount)

	// Apply pagination
	var sessionAggs []SessionAggregation
	if err := query.Offset(offset).Limit(limit).Scan(&sessionAggs).Error; err != nil {
		log.Printf("Failed to fetch sessions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve sessions"})
		return
	}

	// Fetch recording durations for these sessions
	sessionIDs := make([]string, 0, len(sessionAggs))
	for _, sess := range sessionAggs {
		sessionIDs = append(sessionIDs, sess.SessionID)
	}

	var recordings []SessionRecording
	recordingDurations := make(map[string]int)
	if len(sessionIDs) > 0 {
		s.db.Where("session_id IN ? AND project_id = ?", sessionIDs, projectID).Find(&recordings)
		for _, rec := range recordings {
			recordingDurations[rec.SessionID] = rec.Duration
		}
	}

	// Build response
	sessions := make([]map[string]interface{}, 0, len(sessionAggs))
	for _, sess := range sessionAggs {
		duration := int(sess.EndTime.Sub(sess.StartTime).Seconds())
		if recDuration, exists := recordingDurations[sess.SessionID]; exists && recDuration > 0 {
			duration = recDuration
		}

		location := sess.Country
		if location == "" {
			location = "Unknown"
		}

		sessions = append(sessions, map[string]interface{}{
			"id":         sess.SessionID,
			"user_id":    sess.UserID,
			"start_time": sess.StartTime,
			"end_time":   sess.EndTime,
			"events":     sess.EventCount,
			"page_views": sess.PageViews,
			"device":     sess.Device,
			"browser":    sess.Browser,
			"location":   location,
			"duration":   duration,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
		"total":    totalCount,
		"limit":    limit,
		"offset":   offset,
	})
}

// getSessionHandler returns detailed information about a specific session
// OPTIMIZED: Removed redundant event fetch - we use direct WHERE session_id query instead
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
// OPTIMIZED: Uses SQL JSON extraction and aggregation instead of loading all events
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

	// SECURITY: Validate project belongs to authenticated account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		log.Printf("🚨 SECURITY: Unauthorized heatmap access - Account: %s, Project: %s", accountID, projectID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return
	}

	// Calculate date range
	startDate := time.Now().AddDate(0, 0, -7)
	endDate := time.Now().Add(24*time.Hour - time.Nanosecond)

	// OPTIMIZED: Use SQL aggregation with JSON extraction instead of loading all events
	type PageStats struct {
		URL         string    `gorm:"column:url"`
		Title       string    `gorm:"column:title"`
		Visits      int64     `gorm:"column:visits"`
		LastUpdated time.Time `gorm:"column:last_updated"`
	}

	var pageStats []PageStats
	err := s.db.Raw(`
		SELECT 
			properties->>'url' as url,
			MAX(COALESCE(NULLIF(properties->>'title', ''), properties->>'url')) as title,
			COUNT(*) as visits,
			MAX(timestamp) as last_updated
		FROM events
		WHERE account_id = ? 
			AND project_id = ?
			AND timestamp BETWEEN ? AND ?
			AND properties->>'url' IS NOT NULL
			AND properties->>'url' != ''
		GROUP BY properties->>'url'
		ORDER BY visits DESC
		LIMIT 100
	`, accountID.(string), projectID, startDate, endDate).Scan(&pageStats).Error

	if err != nil {
		log.Printf("Failed to fetch heatmap pages: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve pages"})
		return
	}

	// Build response
	pages := make([]map[string]interface{}, 0, len(pageStats))
	for _, page := range pageStats {
		title := page.Title
		if title == "" {
			title = page.URL
		}
		pages = append(pages, map[string]interface{}{
			"id":           uuid.New().String(),
			"url":          page.URL,
			"title":        title,
			"visits":       page.Visits,
			"last_updated": page.LastUpdated,
		})
	}

	c.JSON(http.StatusOK, pages)
}

func (s *Server) signupHandler(c *gin.Context) {
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

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

	// Generate verification token
	verificationToken := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour) // Token valid for 24 hours

	// Create account with unverified email
	account := Account{
		ID:                  uuid.New().String(),
		Name:                req.Name,
		Email:               req.Email,
		Password:            string(hashedPassword),
		EmailVerified:       false,
		VerificationToken:   verificationToken,
		VerificationSentAt:  &now,
		VerificationExpires: &expiresAt,
	}

	if err := s.db.Create(&account).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
		return
	}

	// Send verification email (non-blocking)
	go func() {
		if err := s.emailService.SendVerificationEmail(account.Email, account.Name, verificationToken); err != nil {
			log.Printf("Failed to send verification email to %s: %v", account.Email, err)
		}
	}()

	c.JSON(http.StatusCreated, gin.H{
		"message":              "Account created successfully. Please check your email to verify your account.",
		"requiresVerification": true,
		"account": gin.H{
			"id":            account.ID,
			"name":          account.Name,
			"email":         account.Email,
			"emailVerified": account.EmailVerified,
		},
	})
}

// verifyEmailHandler handles email verification
func (s *Server) verifyEmailHandler(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification token is required"})
		return
	}

	// Find account with this verification token
	var account Account
	if err := s.db.Where("verification_token = ?", token).First(&account).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid or expired verification token"})
		return
	}

	// Check if token is expired
	if account.VerificationExpires != nil && time.Now().After(*account.VerificationExpires) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification token has expired. Please request a new one."})
		return
	}

	// Check if already verified
	if account.EmailVerified {
		c.JSON(http.StatusOK, gin.H{
			"message":  "Email already verified",
			"verified": true,
		})
		return
	}

	// Mark email as verified and clear verification token
	if err := s.db.Model(&account).Updates(map[string]interface{}{
		"email_verified":     true,
		"verification_token": "",
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify email"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Email verified successfully",
		"verified": true,
	})
}

// resendVerificationHandler resends the verification email
func (s *Server) resendVerificationHandler(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Find account
	var account Account
	if err := s.db.Where("email = ?", req.Email).First(&account).Error; err != nil {
		// Don't reveal if email exists or not for security
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists with this email, a verification link has been sent."})
		return
	}

	// Check if already verified
	if account.EmailVerified {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email is already verified"})
		return
	}

	// Generate new verification token
	newToken := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	// Update account with new token
	if err := s.db.Model(&account).Updates(map[string]interface{}{
		"verification_token":   newToken,
		"verification_sent_at": now,
		"verification_expires": expiresAt,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate new verification token"})
		return
	}

	// Send verification email (non-blocking)
	go func() {
		if err := s.emailService.SendVerificationEmail(account.Email, account.Name, newToken); err != nil {
			log.Printf("Failed to resend verification email to %s: %v", account.Email, err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "If an account exists with this email, a verification link has been sent."})
}

// GoogleAuthRequest represents the request for Google OAuth
type GoogleAuthRequest struct {
	IDToken string `json:"idToken" binding:"required"`
}

// googleAuthHandler handles Google OAuth authentication
func (s *Server) googleAuthHandler(c *gin.Context) {
	var req GoogleAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify Google ID token
	// We'll use Google's tokeninfo endpoint for simplicity
	resp, err := http.Get("https://oauth2.googleapis.com/tokeninfo?id_token=" + req.IDToken)
	if err != nil {
		log.Printf("Failed to verify Google ID token: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to verify Google token"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Google token"})
		return
	}

	var tokenInfo struct {
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Sub           string `json:"sub"` // Google user ID
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenInfo); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse Google token info"})
		return
	}

	if tokenInfo.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No email in Google token"})
		return
	}

	// Normalize email
	tokenInfo.Email = strings.ToLower(strings.TrimSpace(tokenInfo.Email))

	// Try to find existing account
	var account Account
	var isNewAccount bool

	if err := s.db.Where("email = ?", tokenInfo.Email).First(&account).Error; err != nil {
		// Create new account
		account = Account{
			ID:            uuid.New().String(),
			Name:          tokenInfo.Name,
			Email:         tokenInfo.Email,
			GoogleID:      tokenInfo.Sub,
			EmailVerified: true, // Google-authenticated emails are pre-verified
			Password:      "",   // No password for OAuth accounts
		}

		if err := s.db.Create(&account).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
			return
		}
		isNewAccount = true
	} else {
		// Update existing account with Google ID if not set
		if account.GoogleID == "" {
			s.db.Model(&account).Update("google_id", tokenInfo.Sub)
		}
		// Mark email as verified if logging in via Google
		if !account.EmailVerified {
			s.db.Model(&account).Update("email_verified", true)
		}
	}

	// Generate access token
	accessToken, err := GenerateJWT(account.ID, account.Email, "access", account.IsAdmin, "owner", 1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := GenerateJWT(account.ID, account.Email, "refresh", account.IsAdmin, "owner", 24*7)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	// Store refresh token
	refreshTokenRecord := RefreshToken{
		ID:        uuid.New().String(),
		Token:     refreshToken,
		AccountID: account.ID,
		ExpiresAt: time.Now().Add(24 * 7 * time.Hour),
		IsRevoked: false,
	}
	s.db.Create(&refreshTokenRecord)

	// Get user's projects
	var projects []Project
	var projectID string
	if err := s.db.Where("account_id = ?", account.ID).Find(&projects).Error; err == nil && len(projects) > 0 {
		projectID = projects[0].ID
	}

	// Check subscription status
	var subscription AccountSubscription
	hasActiveSubscription := false
	subscriptionStatus := "none"
	if err := s.db.Where("account_id = ?", account.ID).First(&subscription).Error; err == nil {
		subscriptionStatus = subscription.Status
		if subscription.Status == "active" || subscription.Status == "trialing" || subscription.Tier == "developer" {
			hasActiveSubscription = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  accessToken,
		"refreshToken": refreshToken,
		"expiresIn":    3600, // 1 hour
		"projectId":    projectID,
		"isNewAccount": isNewAccount,
		"user": gin.H{
			"id":                    account.ID,
			"name":                  account.Name,
			"email":                 account.Email,
			"isAdmin":               account.IsAdmin,
			"hasActiveSubscription": hasActiveSubscription,
			"subscriptionStatus":    subscriptionStatus,
			"emailVerified":         true,
			"role":                  "owner",
		},
	})
}

// forgotPasswordHandler handles password reset requests
func (s *Server) forgotPasswordHandler(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Find account (don't reveal if email exists for security)
	var account Account
	if err := s.db.Where("email = ?", req.Email).First(&account).Error; err != nil {
		// Don't reveal if email exists
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists with this email, a password reset link has been sent."})
		return
	}

	// Check if this is a Google-only account (no password)
	if account.Password == "" && account.GoogleID != "" {
		c.JSON(http.StatusOK, gin.H{"message": "If an account exists with this email, a password reset link has been sent."})
		return
	}

	// Generate reset token
	resetToken := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(1 * time.Hour) // 1 hour expiry

	// Update account with reset token
	if err := s.db.Model(&account).Updates(map[string]interface{}{
		"reset_password_token":   resetToken,
		"reset_password_sent_at": now,
		"reset_password_expires": expiresAt,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate reset token"})
		return
	}

	// Send password reset email (non-blocking)
	go func() {
		if err := s.emailService.SendPasswordResetEmail(account.Email, account.Name, resetToken); err != nil {
			log.Printf("Failed to send password reset email to %s: %v", account.Email, err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "If an account exists with this email, a password reset link has been sent."})
}

// resetPasswordHandler handles password reset with token
func (s *Server) resetPasswordHandler(c *gin.Context) {
	var req struct {
		Token       string `json:"token" binding:"required"`
		NewPassword string `json:"newPassword" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Find account with this reset token
	var account Account
	if err := s.db.Where("reset_password_token = ?", req.Token).First(&account).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid or expired reset token"})
		return
	}

	// Check if token is expired
	if account.ResetPasswordExpires != nil && time.Now().After(*account.ResetPasswordExpires) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Reset token has expired. Please request a new one."})
		return
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Update password and clear reset token
	if err := s.db.Model(&account).Updates(map[string]interface{}{
		"password":               string(hashedPassword),
		"reset_password_token":   "",
		"reset_password_expires": nil,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password reset successfully. You can now sign in with your new password."})
}

func (s *Server) getMeHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get account details
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Get user's projects
	var projects []Project
	var projectID string
	if err := s.db.Where("account_id = ?", account.ID).Find(&projects).Error; err == nil && len(projects) > 0 {
		projectID = projects[0].ID
	}

	// Check subscription status
	var subscription AccountSubscription
	hasActiveSubscription := false
	subscriptionStatus := "none"
	if err := s.db.Where("account_id = ?", account.ID).First(&subscription).Error; err == nil {
		subscriptionStatus = subscription.Status
		// Consider active, trialing, or developer tier as having subscription
		if subscription.Status == "active" || subscription.Status == "trialing" || subscription.Tier == "developer" {
			hasActiveSubscription = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                    account.ID,
		"name":                  account.Name,
		"email":                 account.Email,
		"isAdmin":               account.IsAdmin,
		"projectId":             projectID,
		"hasActiveSubscription": hasActiveSubscription,
		"subscriptionStatus":    subscriptionStatus,
	})
}

func (s *Server) loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Normalize email
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	// Try to find team member user first
	var user User
	var accountID string
	var userName string
	var userEmail string
	var isAdmin bool
	var userRole string
	var hashedPassword string
	var isAccountOwner bool

	if err := s.db.Where("email = ? AND is_active = ?", req.Email, true).First(&user).Error; err == nil {
		// Found a user record
		accountID = user.AccountID
		userName = user.FullName
		userEmail = user.Email
		userRole = user.Role
		hashedPassword = user.Password

		// Check if this user is also an account owner (owner role or password is empty)
		// Account owners might have User records but passwords are stored in Account table
		if user.Role == "owner" || hashedPassword == "" {
			var account Account
			if err := s.db.Where("id = ? AND email = ?", user.AccountID, req.Email).First(&account).Error; err == nil {
				// Use account's password for verification
				hashedPassword = account.Password
				isAdmin = account.IsAdmin
				isAccountOwner = true
				if userName == "" {
					userName = account.Name
				}
			}
		}

		if !isAccountOwner {
			isAdmin = false // Team members are not admins by default
		}
	} else {
		// Try to find account owner directly
		var account Account
		if err := s.db.Where("email = ?", req.Email).First(&account).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}
		accountID = account.ID
		userName = account.Name
		userEmail = account.Email
		isAdmin = account.IsAdmin
		userRole = "owner" // Account owners always have owner role
		hashedPassword = account.Password
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Check email verification status
	// - Account owners need to verify their email explicitly
	// - Team members (invited users) are considered verified since they accepted an invitation sent to their email
	// - Google OAuth users are always considered verified
	var account Account
	emailVerified := true // Default to true
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err == nil {
		// Only require verification for account owners using credential-based login
		// Team members (non-owner roles) have implicitly verified their email by accepting an invitation
		if userRole == "owner" && account.GoogleID == "" {
			emailVerified = account.EmailVerified
		}
		// Team members (admin, member, viewer roles) are always considered verified
		// since they clicked on an email invitation link to join
	}

	// Generate access token (1 hour expiration) and refresh token (7 days expiration)
	accessToken, err := GenerateJWT(accountID, userEmail, "access", isAdmin, userRole, 1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	refreshToken, err := GenerateJWT(accountID, userEmail, "refresh", isAdmin, userRole, 24*7)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	// Store refresh token in database
	refreshTokenRecord := RefreshToken{
		ID:        uuid.New().String(),
		Token:     refreshToken,
		AccountID: accountID,
		ExpiresAt: time.Now().Add(24 * 7 * time.Hour),
		IsRevoked: false,
	}

	if err := s.db.Create(&refreshTokenRecord).Error; err != nil {
		log.Printf("Failed to store refresh token: %v", err)
		// Continue even if storage fails
	}

	// Get user's projects to return the first project ID
	var projects []Project
	var projectID string
	if err := s.db.Where("account_id = ?", accountID).Find(&projects).Error; err == nil && len(projects) > 0 {
		projectID = projects[0].ID
	}

	// Check subscription status
	var subscription AccountSubscription
	hasActiveSubscription := false
	subscriptionStatus := "none"
	if err := s.db.Where("account_id = ?", accountID).First(&subscription).Error; err == nil {
		subscriptionStatus = subscription.Status
		// Consider active, trialing, or developer tier as having subscription
		if subscription.Status == "active" || subscription.Status == "trialing" || subscription.Tier == "developer" {
			hasActiveSubscription = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  accessToken,
		"refreshToken": refreshToken,
		"expiresIn":    3600, // 1 hour in seconds
		"projectId":    projectID,
		"user": gin.H{
			"id":                    accountID,
			"name":                  userName,
			"email":                 userEmail,
			"isAdmin":               isAdmin,
			"role":                  userRole,
			"hasActiveSubscription": hasActiveSubscription,
			"subscriptionStatus":    subscriptionStatus,
			"emailVerified":         emailVerified,
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

	// First, try to find by API key (check cache first)
	if cachedKey, found := s.getCachedAPIKey(token); found && cachedKey.IsActive {
		// Get project from cache or DB
		var project *Project
		if cachedProject, found := s.getCachedProject(cachedKey.ProjectID); found {
			project = cachedProject
		} else {
			var proj Project
			if err := s.db.Where("id = ?", cachedKey.ProjectID).First(&proj).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
				return
			}
			project = &proj
			s.setCachedProject(project)
		}

		// Check if this account is an admin (check cache first)
		isAdmin := false
		if cachedAccount, found := s.getCachedAccount(project.AccountID); found {
			isAdmin = cachedAccount.IsAdmin
		} else {
			var acc Account
			if err := s.db.Where("id = ?", project.AccountID).First(&acc).Error; err == nil {
				isAdmin = acc.IsAdmin
				s.setCachedAccount(&acc)
			}
		}

		if isAdmin {
			c.Set("is_admin", true)
		}

		c.Set("account_id", project.AccountID)
		c.Set("api_key_id", cachedKey.ID)

		// Validate X-Project-ID header if provided
		if projectID != "" {
			// Verify the project belongs to this account
			var targetProject Project
			if err := s.db.Where("id = ? AND account_id = ?", projectID, project.AccountID).First(&targetProject).Error; err != nil {
				log.Printf("🚨 SECURITY: Unauthorized project access attempt - Account: %s, Attempted Project: %s, API Key: %s",
					project.AccountID, projectID, cachedKey.ID)
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
				return
			}
			c.Set("project_id", projectID)
		} else {
			c.Set("project_id", cachedKey.ProjectID)
		}
		c.Next()
		return
	}

	// Not in cache, query database
	var apiKey APIKey
	if err := s.db.Where("key = ? AND is_active = ?", token, true).First(&apiKey).Error; err == nil {
		// Cache the API key for future requests
		s.setCachedAPIKey(&apiKey)

		// Valid API key found - get the associated account through project
		var project Project
		if cachedProject, found := s.getCachedProject(apiKey.ProjectID); found {
			project = *cachedProject
		} else {
			if err := s.db.Where("id = ?", apiKey.ProjectID).First(&project).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
				return
			}
			s.setCachedProject(&project)
		}

		// Check if this account is an admin (check cache first)
		isAdmin := false
		if cachedAccount, found := s.getCachedAccount(project.AccountID); found {
			isAdmin = cachedAccount.IsAdmin
		} else {
			var account Account
			if err := s.db.Where("id = ?", project.AccountID).First(&account).Error; err == nil {
				isAdmin = account.IsAdmin
				s.setCachedAccount(&account)
			}
		}

		if isAdmin {
			c.Set("is_admin", true)
		}

		c.Set("account_id", project.AccountID)
		c.Set("api_key_id", apiKey.ID)

		// Validate X-Project-ID header if provided
		if projectID != "" {
			// Verify the project belongs to this account
			var targetProject Project
			if err := s.db.Where("id = ? AND account_id = ?", projectID, project.AccountID).First(&targetProject).Error; err != nil {
				log.Printf("🚨 SECURITY: Unauthorized project access attempt - Account: %s, Attempted Project: %s, API Key: %s",
					project.AccountID, projectID, apiKey.ID)
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
				return
			}
			c.Set("project_id", projectID)
		} else {
			c.Set("project_id", apiKey.ProjectID)
		}
		c.Next()
		return
	}

	// Try to validate JWT token
	claims, err := ValidateJWT(token)
	if err == nil {
		// Valid JWT token
		c.Set("account_id", claims.AccountID)
		c.Set("email", claims.Email)

		// Set role from claims, default to owner if not present
		role := claims.Role
		if role == "" {
			role = "owner"
		}
		c.Set("role", role)

		// Verify admin status (check cache first, then database)
		isAdmin := false
		if cachedAccount, found := s.getCachedAccount(claims.AccountID); found {
			isAdmin = cachedAccount.IsAdmin
		} else {
			var account Account
			if err := s.db.Where("id = ?", claims.AccountID).First(&account).Error; err == nil {
				isAdmin = account.IsAdmin
				s.setCachedAccount(&account)
			} else {
				// Fallback to JWT claim if account not found
				isAdmin = claims.IsAdmin
			}
		}

		if isAdmin {
			c.Set("is_admin", true)
		}

		// Look up user_id for team member operations
		var user User
		if err := s.db.Where("account_id = ? AND email = ?", claims.AccountID, claims.Email).First(&user).Error; err == nil {
			c.Set("user_id", user.ID)
		}

		// Validate X-Project-ID header if provided
		if projectID != "" {
			// CRITICAL: Verify the project belongs to this account
			var targetProject Project
			if err := s.db.Where("id = ? AND account_id = ?", projectID, claims.AccountID).First(&targetProject).Error; err != nil {
				log.Printf("🚨 SECURITY: Unauthorized project access attempt - Account: %s (JWT), Email: %s, Attempted Project: %s",
					claims.AccountID, claims.Email, projectID)
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
				return
			}
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

	// Look up user_id for team member operations (backward compatibility path)
	var user User
	if err := s.db.Where("account_id = ? AND email = ?", account.ID, account.Email).First(&user).Error; err == nil {
		c.Set("user_id", user.ID)
	}

	// Validate X-Project-ID header if provided
	if projectID != "" {
		// CRITICAL: Verify the project belongs to this account
		var targetProject Project
		if err := s.db.Where("id = ? AND account_id = ?", projectID, account.ID).First(&targetProject).Error; err != nil {
			log.Printf("🚨 SECURITY: Unauthorized project access attempt - Account: %s (Email Token), Email: %s, Attempted Project: %s",
				account.ID, account.Email, projectID)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
			return
		}
		c.Set("project_id", projectID)
	}
	c.Next()
}

// adminMiddleware checks if the authenticated user is an admin
func (s *Server) adminMiddleware(c *gin.Context) {
	isAdmin, exists := c.Get("is_admin")
	if !exists || !isAdmin.(bool) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return
	}
	c.Next()
}

func (s *Server) listProjectsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")

	// Try to get from cache first
	cacheKey := fmt.Sprintf("account_projects:%s", accountID.(string))
	if projects, found := s.projectListCache.Get(cacheKey); found {
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
		return
	}

	var projects []Project
	if err := s.db.Where("account_id = ?", accountID.(string)).Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}

	// Cache the projects list
	s.projectListCache.Set(cacheKey, projects, 10*time.Minute)

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
	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can create projects"})
		return
	}

	// Check existing project count for non-enterprise users
	var existingProjects []Project
	if err := s.db.Where("account_id = ?", accountID.(string)).Find(&existingProjects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing projects"})
		return
	}

	// Get account subscription to check tier
	var subscription AccountSubscription
	isEnterprise := false
	if err := s.db.Where("account_id = ?", accountID.(string)).First(&subscription).Error; err == nil {
		isEnterprise = subscription.Tier == "enterprise"
	}

	// Limit non-enterprise users to 1 project
	if !isEnterprise && len(existingProjects) >= 1 {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "Project limit reached. Upgrade to Enterprise plan to create multiple projects.",
			"limit":   1,
			"current": len(existingProjects),
		})
		return
	}

	project := Project{
		ID:        uuid.New().String(),
		Name:      req.Name,
		AccountID: accountID.(string),
	}

	if err := s.db.Create(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project"})
		return
	}
	// Cache the new project and invalidate account projects list
	s.setCachedProject(&project)
	cacheKey := fmt.Sprintf("account_projects:%s", accountID.(string))
	s.projectListCache.Delete(cacheKey)

	// Auto-create a default API key for the new project
	apiKey := APIKey{
		ID:        uuid.New().String(),
		Key:       "mentiq_live_" + uuid.New().String(),
		Name:      "Default API Key",
		ProjectID: project.ID,
		IsActive:  true,
	}
	if err := s.db.Create(&apiKey).Error; err != nil {
		// Log error but don't fail the project creation
		log.Printf("Warning: Failed to create default API key for project %s: %v", project.ID, err)
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":        project.ID,
		"name":      project.Name,
		"accountId": project.AccountID,
		"createdAt": project.CreatedAt,
		"updatedAt": project.UpdatedAt,
		"apiKey":    apiKey.Key, // Return the auto-generated API key
	})
}

func (s *Server) updateProjectHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, _ := c.Get("account_id")
	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can update projects"})
		return
	}

	var req UpdateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Update fields if provided
	if req.Name != nil {
		project.Name = *req.Name
	}

	if err := s.db.Save(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
		return
	}

	// Invalidate caches
	s.invalidateProjectCache(project.ID)
	cacheKey := fmt.Sprintf("account_projects:%s", accountID.(string))
	s.projectListCache.Delete(cacheKey)

	c.JSON(http.StatusOK, gin.H{
		"id":        project.ID,
		"name":      project.Name,
		"accountId": project.AccountID,
		"createdAt": project.CreatedAt,
		"updatedAt": project.UpdatedAt,
	})
}

func (s *Server) deleteProjectHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID, _ := c.Get("account_id")
	email := c.GetString("email")

	// Check user role - only owners can delete projects
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || userRole != "owner" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only account owners can delete projects"})
		return
	}

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Delete associated API keys first
	if err := s.db.Where("project_id = ?", projectID).Delete(&APIKey{}).Error; err != nil {
		log.Printf("Failed to delete API keys for project %s: %v", projectID, err)
	}

	// Delete the project
	if err := s.db.Delete(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete project"})
		return
	}

	// Invalidate caches
	s.invalidateProjectCache(projectID)
	cacheKey := fmt.Sprintf("account_projects:%s", accountID.(string))
	s.projectListCache.Delete(cacheKey)

	c.JSON(http.StatusOK, gin.H{"message": "Project deleted successfully"})
}

func (s *Server) refreshTokenHandler(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate refresh token
	claims, err := ValidateJWT(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	// Ensure it's a refresh token
	if claims.Type != "refresh" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token type"})
		return
	}

	// Check if refresh token exists and is not revoked
	var refreshTokenRecord RefreshToken
	if err := s.db.Where("token = ? AND is_revoked = ?", req.RefreshToken, false).First(&refreshTokenRecord).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Refresh token not found or revoked"})
		return
	}

	// Check if refresh token is expired
	if time.Now().After(refreshTokenRecord.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Refresh token expired"})
		return
	}

	// Get role from claims, default to owner if not present (for backward compatibility)
	role := claims.Role
	if role == "" {
		role = "owner"
	}

	// Generate new access token
	newAccessToken, err := GenerateJWT(claims.AccountID, claims.Email, "access", claims.IsAdmin, role, 1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate access token"})
		return
	}

	// Optionally generate new refresh token (refresh token rotation)
	newRefreshToken, err := GenerateJWT(claims.AccountID, claims.Email, "refresh", claims.IsAdmin, role, 24*7)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate refresh token"})
		return
	}

	// Revoke old refresh token
	if err := s.db.Model(&refreshTokenRecord).Update("is_revoked", true).Error; err != nil {
		log.Printf("Failed to revoke old refresh token: %v", err)
	}

	// Store new refresh token
	newRefreshTokenRecord := RefreshToken{
		ID:        uuid.New().String(),
		Token:     newRefreshToken,
		AccountID: claims.AccountID,
		ExpiresAt: time.Now().Add(24 * 7 * time.Hour),
		IsRevoked: false,
	}

	if err := s.db.Create(&newRefreshTokenRecord).Error; err != nil {
		log.Printf("Failed to store new refresh token: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  newAccessToken,
		"refreshToken": newRefreshToken,
		"expiresIn":    3600, // 1 hour in seconds
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
	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can create API keys"})
		return
	}

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

	// Cache the new API key
	s.setCachedAPIKey(&apiKey)

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
	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can update API keys"})
		return
	}

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
		// Invalidate the cache for this API key
		s.invalidateAPIKeyCache(apiKey.Key)
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
	email := c.GetString("email")

	// Check user role - require member or above
	userRole, err := s.getUserRole(accountID.(string), email)
	if err != nil || (userRole != "owner" && userRole != "admin" && userRole != "member") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only members and above can delete API keys"})
		return
	}

	// Verify project exists and belongs to user
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID.(string)).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Fetch the API key to get the token before deleting
	var apiKeyToDelete APIKey
	if err := s.db.Where("id = ? AND project_id = ?", keyID, projectID).First(&apiKeyToDelete).Error; err == nil {
		// Invalidate cache before deleting
		s.invalidateAPIKeyCache(apiKeyToDelete.Key)
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

// Admin Handlers

// adminListAccountsHandler lists all accounts (admin only)
func (s *Server) adminListAccountsHandler(c *gin.Context) {
	var accounts []Account
	if err := s.db.Find(&accounts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch accounts"})
		return
	}

	// Don't expose passwords in response - initialize to empty slice to return [] instead of null
	response := make([]gin.H, 0, len(accounts))
	for _, account := range accounts {
		response = append(response, gin.H{
			"id":               account.ID,
			"name":             account.Name,
			"email":            account.Email,
			"isAdmin":          account.IsAdmin,
			"stripeCustomerId": account.StripeCustomerID,
			"createdAt":        account.CreatedAt,
			"updatedAt":        account.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, response)
}

// adminGetAccountHandler gets a specific account (admin only)
func (s *Server) adminGetAccountHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":               account.ID,
		"name":             account.Name,
		"email":            account.Email,
		"isAdmin":          account.IsAdmin,
		"stripeCustomerId": account.StripeCustomerID,
		"createdAt":        account.CreatedAt,
		"updatedAt":        account.UpdatedAt,
	})
}

// adminListAccountProjectsHandler lists all projects for a specific account (admin only)
func (s *Server) adminListAccountProjectsHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var projects []Project
	if err := s.db.Where("account_id = ?", accountID).Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}

	response := make([]gin.H, 0, len(projects))
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

// adminListAccountUsersHandler lists all users for a specific account (admin only)
// In this system, each account IS a user, so we return the account itself
func (s *Server) adminListAccountUsersHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch account"})
		return
	}

	// Return the account as a user (account = user in this system)
	response := []gin.H{
		{
			"id":         account.ID,
			"email":      account.Email,
			"account_id": account.ID,
			"created_at": account.CreatedAt,
			"updated_at": account.UpdatedAt,
		},
	}

	c.JSON(http.StatusOK, response)
}

// adminListAllProjectsHandler lists all projects across all accounts (admin only)
func (s *Server) adminListAllProjectsHandler(c *gin.Context) {
	var projects []Project
	if err := s.db.Preload("Account").Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}

	response := make([]gin.H, 0, len(projects))
	for _, project := range projects {
		response = append(response, gin.H{
			"id":        project.ID,
			"name":      project.Name,
			"accountId": project.AccountID,
			"account": gin.H{
				"id":    project.Account.ID,
				"name":  project.Account.Name,
				"email": project.Account.Email,
			},
			"createdAt": project.CreatedAt,
			"updatedAt": project.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, response)
}

// adminListAllEventsHandler lists events with pagination (admin only)
func (s *Server) adminListAllEventsHandler(c *gin.Context) {
	// Get pagination parameters
	limit := 100
	offset := 0
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// Optional filters
	accountID := c.Query("account_id")
	projectID := c.Query("project_id")
	eventType := c.Query("event_type")

	query := s.db.Model(&Event{})
	if accountID != "" {
		query = query.Where("account_id = ?", accountID)
	}
	if projectID != "" {
		query = query.Where("project_id = ?", projectID)
	}
	if eventType != "" {
		query = query.Where("event_type = ?", eventType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count events"})
		return
	}

	var events []Event
	if err := query.Order("timestamp DESC").Limit(limit).Offset(offset).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"events": events,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// adminToggleAdminHandler toggles admin status for an account (admin only)
func (s *Server) adminToggleAdminHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	type ToggleAdminRequest struct {
		IsAdmin bool `json:"is_admin"`
	}

	var req ToggleAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Update admin status
	if err := s.db.Model(&account).Update("is_admin", req.IsAdmin).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update admin status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      account.ID,
		"email":   account.Email,
		"isAdmin": req.IsAdmin,
		"message": "Admin status updated successfully",
	})
}

// adminGetUserDataHandler gets all data for a specific user (admin only)
func (s *Server) adminGetUserDataHandler(c *gin.Context) {
	userID := c.Param("user_id")

	// Get user events
	var events []Event
	if err := s.db.Where("user_id = ?", userID).Order("timestamp DESC").Limit(100).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user events"})
		return
	}

	// Get user info from events (assuming user_id is tracked)
	var eventCount int64
	s.db.Model(&Event{}).Where("user_id = ?", userID).Count(&eventCount)

	// Get unique sessions
	var sessions []struct {
		SessionID string
		Count     int64
	}
	s.db.Model(&Event{}).
		Select("session_id, COUNT(*) as count").
		Where("user_id = ? AND session_id IS NOT NULL AND session_id != ''", userID).
		Group("session_id").
		Find(&sessions)

	// Get event types breakdown
	var eventTypes []struct {
		EventType string
		Count     int64
	}
	s.db.Model(&Event{}).
		Select("event_type, COUNT(*) as count").
		Where("user_id = ?", userID).
		Group("event_type").
		Order("count DESC").
		Find(&eventTypes)

	c.JSON(http.StatusOK, gin.H{
		"userId":         userID,
		"totalEvents":    eventCount,
		"totalSessions":  len(sessions),
		"recentEvents":   events,
		"sessions":       sessions,
		"eventBreakdown": eventTypes,
	})
}

// adminGetUserProjectsHandler gets all projects for a specific user/account (admin only)
func (s *Server) adminGetUserProjectsHandler(c *gin.Context) {
	userID := c.Param("user_id")

	// user_id is actually account_id in our system
	var projects []Project
	if err := s.db.Where("account_id = ?", userID).Find(&projects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user projects"})
		return
	}

	c.JSON(http.StatusOK, projects)
}

// adminGetAllUsersWithProjectsHandler gets all users with their projects in a single efficient call (admin only)
func (s *Server) adminGetAllUsersWithProjectsHandler(c *gin.Context) {
	// Get all accounts
	var accounts []Account
	if err := s.db.Find(&accounts).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch accounts"})
		return
	}

	// Get all projects
	var allProjects []Project
	if err := s.db.Find(&allProjects).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}

	// Group projects by account_id
	projectsByAccount := make(map[string][]Project)
	for _, project := range allProjects {
		projectsByAccount[project.AccountID] = append(projectsByAccount[project.AccountID], project)
	}

	// Build response with users and their projects
	type UserWithProjects struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		AccountID string    `json:"account_id"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Projects  []Project `json:"projects"`
	}

	response := make([]UserWithProjects, 0, len(accounts))
	for _, account := range accounts {
		projects := projectsByAccount[account.ID]
		if projects == nil {
			projects = []Project{}
		}
		response = append(response, UserWithProjects{
			ID:        account.ID,
			Email:     account.Email,
			AccountID: account.ID,
			CreatedAt: account.CreatedAt,
			UpdatedAt: account.UpdatedAt,
			Projects:  projects,
		})
	}

	c.JSON(http.StatusOK, response)
}

// adminGetProjectDataHandler gets detailed analytics data for a specific project (admin only)
func (s *Server) adminGetProjectDataHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Get project info
	var project Project
	if err := s.db.Where("id = ?", projectID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Get date range from query params (default to last 30 days)
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02")
	}

	// Get events for this project
	var events []Event
	if err := s.db.Where("project_id = ? AND timestamp >= ? AND timestamp <= ?",
		projectID, startDate+" 00:00:00", endDate+" 23:59:59").
		Order("timestamp DESC").Limit(1000).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project events"})
		return
	}

	// Calculate metrics
	totalEvents := len(events)
	uniqueUsers := make(map[string]bool)
	uniqueSessions := make(map[string]bool)
	eventTypeMap := make(map[string]int)
	deviceMap := make(map[string]int)
	browserMap := make(map[string]int)
	countryMap := make(map[string]int)

	for _, event := range events {
		if event.UserID != "" {
			uniqueUsers[event.UserID] = true
		}
		if event.SessionID != "" {
			uniqueSessions[event.SessionID] = true
		}
		eventTypeMap[event.EventType]++
		if event.Device != "" {
			deviceMap[event.Device]++
		}
		if event.Browser != "" {
			browserMap[event.Browser]++
		}
		if event.Country != "" {
			countryMap[event.Country]++
		}
	}

	// Convert maps to arrays
	var eventBreakdown []map[string]interface{}
	for eventType, count := range eventTypeMap {
		eventBreakdown = append(eventBreakdown, map[string]interface{}{
			"event_type": eventType,
			"count":      count,
		})
	}

	var deviceBreakdown []map[string]interface{}
	for device, count := range deviceMap {
		deviceBreakdown = append(deviceBreakdown, map[string]interface{}{
			"device": device,
			"count":  count,
		})
	}

	var browserBreakdown []map[string]interface{}
	for browser, count := range browserMap {
		browserBreakdown = append(browserBreakdown, map[string]interface{}{
			"browser": browser,
			"count":   count,
		})
	}

	var countryBreakdown []map[string]interface{}
	for country, count := range countryMap {
		countryBreakdown = append(countryBreakdown, map[string]interface{}{
			"country": country,
			"count":   count,
		})
	}

	// Get recent events (limit to 50)
	recentEventsLimit := 50
	if len(events) < recentEventsLimit {
		recentEventsLimit = len(events)
	}

	c.JSON(http.StatusOK, gin.H{
		"project":           project,
		"total_events":      totalEvents,
		"unique_users":      len(uniqueUsers),
		"unique_sessions":   len(uniqueSessions),
		"event_breakdown":   eventBreakdown,
		"device_breakdown":  deviceBreakdown,
		"browser_breakdown": browserBreakdown,
		"country_breakdown": countryBreakdown,
		"recent_events":     events[:recentEventsLimit],
		"start_date":        startDate,
		"end_date":          endDate,
	})
}

// Test/Debug Handlers - Remove or disable in production!

// testListAllRecordingsHandler lists all recordings (no auth) for testing
func (s *Server) testListAllRecordingsHandler(c *gin.Context) {
	var recordings []SessionRecording

	limit := 50
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	if err := s.db.Order("created_at DESC").Limit(limit).Find(&recordings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch recordings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recordings": recordings,
		"total":      len(recordings),
		"message":    "Test endpoint - disable in production",
	})
}

// testGetRecordingBySessionHandler gets a specific recording by session ID (no auth) for testing
func (s *Server) testGetRecordingBySessionHandler(c *gin.Context) {
	sessionID := c.Param("session_id")

	var recording SessionRecording
	if err := s.db.Where("session_id = ?", sessionID).First(&recording).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch recording"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse recording data"})
			return
		}
	} else {
		events = []map[string]interface{}{}
	}

	storageType := "Database"
	if recording.StoragePath != "" {
		storageType = "S3/R2"
	}

	c.JSON(http.StatusOK, gin.H{
		"recording":    recording,
		"events":       events,
		"event_count":  len(events),
		"storage_type": storageType,
		"message":      "Test endpoint - disable in production",
	})
}

// adminCreateTestUserHandler creates a test user that bypasses email verification and paywall (admin only)
func (s *Server) adminCreateTestUserHandler(c *gin.Context) {
	// Parse request body
	var req struct {
		Name                  string `json:"name" binding:"required"`
		Email                 string `json:"email" binding:"required,email"`
		Password              string `json:"password" binding:"required,min=8"`
		SkipEmailVerification bool   `json:"skip_email_verification"`
		SkipPaywall           bool   `json:"skip_paywall"`
		CreateProject         bool   `json:"create_project"`
		ProjectName           string `json:"project_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if user already exists
	var existingAccount Account
	if err := s.db.Where("email = ?", req.Email).First(&existingAccount).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User with this email already exists"})
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Create account with test user flags
	accountID := uuid.New().String()
	account := Account{
		ID:            accountID,
		Name:          req.Name,
		Email:         req.Email,
		Password:      string(hashedPassword),
		EmailVerified: req.SkipEmailVerification, // Bypass email verification if requested
		IsAdmin:       false,
	}

	if err := s.db.Create(&account).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
		return
	}

	var projectID string

	// Create a project if requested
	if req.CreateProject {
		projectName := req.ProjectName
		if projectName == "" {
			projectName = "Test Project"
		}

		projectID = uuid.New().String()
		project := Project{
			ID:        projectID,
			Name:      projectName,
			AccountID: accountID,
		}

		if err := s.db.Create(&project).Error; err != nil {
			log.Printf("Failed to create project for test user: %v", err)
			projectID = ""
		}
	}

	// If skip_paywall is true, create a subscription record to bypass paywall
	if req.SkipPaywall {
		subscriptionID := uuid.New().String()
		subscription := AccountSubscription{
			ID:                   subscriptionID,
			AccountID:            accountID,
			Tier:                 "test",
			Status:               "active",
			UserCount:            100,
			MonthlyPrice:         0,
			StripeSubscriptionID: "test_sub_" + accountID[:8],
			CurrentPeriodStart:   time.Now(),
			CurrentPeriodEnd:     time.Now().AddDate(1, 0, 0), // 1 year from now
		}

		if err := s.db.Create(&subscription).Error; err != nil {
			log.Printf("Failed to create subscription for test user: %v", err)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":          "Test user created successfully",
		"account_id":       account.ID,
		"email":            account.Email,
		"email_verified":   account.EmailVerified,
		"has_subscription": req.SkipPaywall,
		"project_id":       projectID,
	})
}
