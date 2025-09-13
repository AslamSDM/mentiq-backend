package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"

	"mentiq-backend/prisma/db"
)

// Setup a test router
func setupRouter() (*gin.Engine, *db.PrismaClient) {
	if err := godotenv.Load(".env.local"); err != nil {
		godotenv.Load(".env")
	}

	router := gin.Default()

	dbClient := db.NewClient()
	if err := dbClient.Connect(); err != nil {
		panic("failed to connect to database")
	}

	return router, dbClient
}

func TestHealthCheckHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, dbClient := setupRouter()
	defer dbClient.Disconnect()

	router.GET("/health", healthCheckHandler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"healthy"`)
}

func TestSignupHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, dbClient := setupRouter()
	defer dbClient.Disconnect()

	router.POST("/signup", signupHandler(dbClient))

	// Clean up created account
	defer func() {
		dbClient.Account.FindMany(
			db.Account.Email.Equals("test@example.com"),
		).Delete().Exec(context.Background())
	}()

	t.Run("Successful signup", func(t *testing.T) {
		signupReq := SignupRequest{
			Name:     "Test User",
			Email:    "test@example.com",
			Password: "password123",
		}
		body, _ := json.Marshal(signupReq)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/signup", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Contains(t, w.Body.String(), "Account created successfully")
	})

	t.Run("Duplicate email", func(t *testing.T) {
		signupReq := SignupRequest{
			Name:     "Test User 2",
			Email:    "test@example.com",
			Password: "password456",
		}
		body, _ := json.Marshal(signupReq)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/signup", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "Email already in use")
	})
}

func TestIngestEventHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, dbClient := setupRouter()
	defer dbClient.Disconnect()

	bucketName := os.Getenv("S3_BUCKET_NAME")
	analyticsService, err := NewAnalyticsService(bucketName, dbClient)
	assert.NoError(t, err)

	// Mock account and project
	account, _ := dbClient.Account.CreateOne(
		db.Account.Name.Set("test-account"),
		db.Account.Email.Set("ingest@test.com"),
		db.Account.Password.Set("password"),
	).Exec(context.Background())
	project, _ := dbClient.Project.CreateOne(
		db.Project.Name.Set("test-project"),
		db.Project.Account.Link(
			db.Account.ID.Equals(account.ID),
		),
	).Exec(context.Background())

	defer func() {
		dbClient.Project.FindMany(db.Project.ID.Equals(project.ID)).Delete().Exec(context.Background())
		dbClient.Account.FindMany(db.Account.ID.Equals(account.ID)).Delete().Exec(context.Background())
	}()

	apiV1 := router.Group("/api/v1")
	apiV1.Use(AuthMiddleware(dbClient))
	{
		apiV1.POST("/events", analyticsService.ingestEventHandler)
	}

	t.Run("Successful event ingestion", func(t *testing.T) {
		event := map[string]interface{}{
			"event_type": "page_view",
			"properties": map[string]string{"path": "/"},
		}
		body, _ := json.Marshal(event)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Event queued for processing")
		assert.Contains(t, w.Body.String(), "cache_size")
	})

	t.Run("Unauthorized request", func(t *testing.T) {
		event := map[string]interface{}{"event_type": "click"}
		body, _ := json.Marshal(event)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestBatchIngestHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, dbClient := setupRouter()
	defer dbClient.Disconnect()

	bucketName := os.Getenv("S3_BUCKET_NAME")
	analyticsService, err := NewAnalyticsService(bucketName, dbClient)
	assert.NoError(t, err)

	// Mock account and project
	account, _ := dbClient.Account.CreateOne(
		db.Account.Name.Set("test-batch-account"),
		db.Account.Email.Set("ingest-batch@test.com"),
		db.Account.Password.Set("password"),
	).Exec(context.Background())
	project, _ := dbClient.Project.CreateOne(
		db.Project.Name.Set("test-batch-project"),
		db.Project.Account.Link(
			db.Account.ID.Equals(account.ID),
		),
	).Exec(context.Background())

	defer func() {
		dbClient.Project.FindMany(db.Project.ID.Equals(project.ID)).Delete().Exec(context.Background())
		dbClient.Account.FindMany(db.Account.ID.Equals(account.ID)).Delete().Exec(context.Background())
	}()

	apiV1 := router.Group("/api/v1")
	apiV1.Use(AuthMiddleware(dbClient))
	{
		apiV1.POST("/events/batch", analyticsService.batchIngestHandler)
		apiV1.POST("/flush-cache", analyticsService.flushCacheHandler)
	}

	t.Run("Successful batch event ingestion", func(t *testing.T) {
		events := []map[string]interface{}{
			{
				"event_type": "page_view",
				"properties": map[string]string{"path": "/"},
			},
			{
				"event_type": "click",
				"properties": map[string]string{"button": "buy"},
			},
		}
		body, _ := json.Marshal(events)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/events/batch", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"success_count":2`)
		assert.Contains(t, w.Body.String(), "cache_size")
	})

	t.Run("Manual cache flush", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/flush-cache", nil)
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		// Cache might be empty or contain events depending on timing
		assert.True(t,
			strings.Contains(w.Body.String(), "Cache flushed successfully") ||
				strings.Contains(w.Body.String(), "Cache is empty"))
	})
}

func TestAnalyticsAndDashboardHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, dbClient := setupRouter()
	defer dbClient.Disconnect()

	bucketName := os.Getenv("S3_BUCKET_NAME")
	analyticsService, err := NewAnalyticsService(bucketName, dbClient)
	assert.NoError(t, err)

	// Mock account and project
	account, _ := dbClient.Account.CreateOne(
		db.Account.Name.Set("test-analytics-account"),
		db.Account.Email.Set("analytics@test.com"),
		db.Account.Password.Set("password"),
	).Exec(context.Background())
	project, _ := dbClient.Project.CreateOne(
		db.Project.Name.Set("test-analytics-project"),
		db.Project.Account.Link(
			db.Account.ID.Equals(account.ID),
		),
	).Exec(context.Background())

	defer func() {
		dbClient.Project.FindMany(db.Project.ID.Equals(project.ID)).Delete().Exec(context.Background())
		dbClient.Account.FindMany(db.Account.ID.Equals(account.ID)).Delete().Exec(context.Background())
	}()

	apiV1 := router.Group("/api/v1")
	apiV1.Use(AuthMiddleware(dbClient))
	{
		apiV1.GET("/analytics", analyticsService.GetAnalyticsHandler)
		apiV1.GET("/dashboard", analyticsService.GetDashboardHandler)
		apiV1.GET("/realtime", analyticsService.GetRealTimeHandler)
	}

	// For now, these tests just check if the endpoints return OK,
	// as they depend on data in S3 which is harder to mock.
	t.Run("GetAnalyticsHandler", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/analytics", nil)
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("GetDashboardHandler", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("GetRealTimeHandler", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/v1/realtime", nil)
		req.Header.Set("Authorization", "ApiKey "+account.ID)
		req.Header.Set("X-Project-ID", project.ID)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}
