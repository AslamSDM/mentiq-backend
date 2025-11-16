package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// R2AnalyticsService handles time-series analytics with R2 storage
type R2AnalyticsService struct {
	s3Client   *s3.S3
	bucketName string
	db         *gorm.DB

	// Multi-level caching system
	eventCache []Event
	cacheMutex sync.RWMutex

	// Data caches with TTL for different data types
	eventsCache    map[string]*CacheEntry // Raw events cache
	heatmapCache   map[string]*CacheEntry // Heatmap data cache
	sessionCache   map[string]*CacheEntry // Session data cache
	recordingCache map[string]*CacheEntry // Recording data cache
	metricsCache   map[string]*CacheEntry // Computed metrics cache
	dashboardCache map[string]*CacheEntry // Dashboard summaries cache
	dataCacheMutex sync.RWMutex

	// Background processing
	flushTicker *time.Ticker
	stopChan    chan bool
}

// TimeSeriesData represents structured time-series data for R2 storage
type TimeSeriesData struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	ProjectID string                 `json:"project_id"`
	AccountID string                 `json:"account_id"`
	DataType  string                 `json:"data_type"` // "events", "heatmaps", "sessions", "recordings"
	Data      map[string]interface{} `json:"data"`
	Version   string                 `json:"version"`
	CreatedAt time.Time              `json:"created_at"`
}

// HeatmapData represents heatmap interaction data
type HeatmapData struct {
	PageURL      string       `json:"page_url"`
	Clicks       []ClickData  `json:"clicks"`
	Scrolls      []ScrollData `json:"scrolls"`
	Hovers       []HoverData  `json:"hovers"`
	ViewportData ViewportInfo `json:"viewport_data"`
}

type ClickData struct {
	X         int       `json:"x"`
	Y         int       `json:"y"`
	Timestamp time.Time `json:"timestamp"`
	Element   string    `json:"element,omitempty"`
	Count     int       `json:"count"`
}

type ScrollData struct {
	Depth     float64   `json:"depth"`
	Timestamp time.Time `json:"timestamp"`
	MaxDepth  float64   `json:"max_depth"`
}

type HoverData struct {
	X        int           `json:"x"`
	Y        int           `json:"y"`
	Duration time.Duration `json:"duration"`
	Element  string        `json:"element,omitempty"`
}

type ViewportInfo struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SessionData represents session analytics data
type SessionData struct {
	SessionID    string         `json:"session_id"`
	UserID       string         `json:"user_id,omitempty"`
	StartTime    time.Time      `json:"start_time"`
	EndTime      time.Time      `json:"end_time,omitempty"`
	Duration     int64          `json:"duration"` // seconds
	PageViews    []PageView     `json:"page_views"`
	Events       []SessionEvent `json:"events"`
	DeviceInfo   DeviceInfo     `json:"device_info"`
	LocationInfo LocationInfo   `json:"location_info"`
	IsActive     bool           `json:"is_active"`
}

type PageView struct {
	URL       string    `json:"url"`
	Title     string    `json:"title,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Duration  int64     `json:"duration,omitempty"` // Time spent on page in seconds
}

type SessionEvent struct {
	EventType  string                 `json:"event_type"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

type DeviceInfo struct {
	Browser   string       `json:"browser"`
	OS        string       `json:"os"`
	Device    string       `json:"device"`
	UserAgent string       `json:"user_agent"`
	Viewport  ViewportInfo `json:"viewport"`
}

type LocationInfo struct {
	Country string `json:"country"`
	City    string `json:"city"`
	IP      string `json:"ip"`
}

// NewR2AnalyticsService creates a new R2-based analytics service
func NewR2AnalyticsService(s3Client *s3.S3, bucketName string, db *gorm.DB) *R2AnalyticsService {
	service := &R2AnalyticsService{
		s3Client:       s3Client,
		bucketName:     bucketName,
		db:             db,
		eventCache:     make([]Event, 0),
		eventsCache:    make(map[string]*CacheEntry),
		heatmapCache:   make(map[string]*CacheEntry),
		sessionCache:   make(map[string]*CacheEntry),
		recordingCache: make(map[string]*CacheEntry),
		metricsCache:   make(map[string]*CacheEntry),
		dashboardCache: make(map[string]*CacheEntry),
		flushTicker:    time.NewTicker(30 * time.Minute),
		stopChan:       make(chan bool),
	}

	// Start background workers
	go service.startBatchProcessor()
	go service.startCacheCleanup()

	return service
}

// R2 Storage Methods

// storeTimeSeriesDataToR2 stores structured time-series data to R2
func (r2s *R2AnalyticsService) storeTimeSeriesDataToR2(data TimeSeriesData) error {
	// Create hierarchical key structure for efficient querying
	key := fmt.Sprintf("timeseries/%s/%s/%s/%d/%02d/%02d/%s_%s.json",
		data.DataType,
		data.AccountID,
		data.ProjectID,
		data.Timestamp.Year(),
		data.Timestamp.Month(),
		data.Timestamp.Day(),
		data.ID,
		data.DataType)

	// Marshal data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal time-series data: %v", err)
	}

	// Store to R2
	_, err = r2s.s3Client.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(r2s.bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(jsonData),
		ContentType: aws.String("application/json"),
		Metadata: map[string]*string{
			"data-type":  aws.String(data.DataType),
			"account-id": aws.String(data.AccountID),
			"project-id": aws.String(data.ProjectID),
			"version":    aws.String(data.Version),
		},
	})

	if err != nil {
		return fmt.Errorf("failed to store time-series data to R2: %v", err)
	}

	log.Printf("Time-series data %s stored to R2 with key: %s", data.ID, key)
	return nil
}

// fetchTimeSeriesDataFromR2 retrieves time-series data from R2 for a date range
func (r2s *R2AnalyticsService) fetchTimeSeriesDataFromR2(accountID, projectID, dataType, startDate, endDate string) ([]TimeSeriesData, error) {
	// Check cache first
	cacheKey := r2s.generateCacheKey("timeseries", accountID, projectID, dataType, startDate, endDate)
	if cachedData, found := r2s.getCachedData(cacheKey, r2s.eventsCache); found {
		return cachedData.([]TimeSeriesData), nil
	}

	var allData []TimeSeriesData

	// Parse dates
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %v", err)
	}

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date: %v", err)
	}

	// Query R2 for each day in the range
	for current := start; !current.After(end); current = current.AddDate(0, 0, 1) {
		prefix := fmt.Sprintf("timeseries/%s/%s/%s/%d/%02d/%02d/",
			dataType,
			accountID,
			projectID,
			current.Year(),
			current.Month(),
			current.Day())

		// List objects with prefix
		result, err := r2s.s3Client.ListObjectsV2(&s3.ListObjectsV2Input{
			Bucket: aws.String(r2s.bucketName),
			Prefix: aws.String(prefix),
		})

		if err != nil {
			log.Printf("Error listing objects for prefix %s: %v", prefix, err)
			continue
		}

		// Fetch each object
		for _, obj := range result.Contents {
			objResult, err := r2s.s3Client.GetObject(&s3.GetObjectInput{
				Bucket: aws.String(r2s.bucketName),
				Key:    obj.Key,
			})

			if err != nil {
				log.Printf("Error fetching object %s: %v", *obj.Key, err)
				continue
			}

			var tsData TimeSeriesData
			if err := json.NewDecoder(objResult.Body).Decode(&tsData); err != nil {
				log.Printf("Error decoding object %s: %v", *obj.Key, err)
				objResult.Body.Close()
				continue
			}

			allData = append(allData, tsData)
			objResult.Body.Close()
		}
	}

	// Cache the results for 15 minutes
	r2s.setCachedData(cacheKey, allData, 15*time.Minute, r2s.eventsCache)

	log.Printf("Fetched %d time-series records from R2 for %s (%s to %s)", len(allData), dataType, startDate, endDate)
	return allData, nil
}

// Event Processing Methods

// ingestEventWithR2 processes and stores events using R2 backend
func (r2s *R2AnalyticsService) ingestEventWithR2(event Event) error {
	// Add to memory cache for batching
	r2s.cacheMutex.Lock()
	r2s.eventCache = append(r2s.eventCache, event)
	r2s.cacheMutex.Unlock()

	// For high-frequency events, batch them for efficient storage
	if len(r2s.eventCache) >= 100 {
		go r2s.flushEventCache()
	}

	return nil
}

// Heatmap Methods

// storeHeatmapData stores heatmap interaction data to R2
func (r2s *R2AnalyticsService) storeHeatmapData(accountID, projectID string, heatmapData HeatmapData) error {
	tsData := TimeSeriesData{
		ID:        uuid.New().String(),
		Timestamp: time.Now().UTC(),
		ProjectID: projectID,
		AccountID: accountID,
		DataType:  "heatmaps",
		Data: map[string]interface{}{
			"heatmap": heatmapData,
		},
		Version:   "1.0",
		CreatedAt: time.Now().UTC(),
	}

	return r2s.storeTimeSeriesDataToR2(tsData)
}

// getHeatmapDataForPage retrieves heatmap data for a specific page
func (r2s *R2AnalyticsService) getHeatmapDataForPage(accountID, projectID, pageURL string, startDate, endDate string) ([]HeatmapData, error) {
	// Check cache first
	cacheKey := r2s.generateCacheKey("heatmap", accountID, projectID, pageURL, startDate, endDate)
	if cachedData, found := r2s.getCachedData(cacheKey, r2s.heatmapCache); found {
		return cachedData.([]HeatmapData), nil
	}

	// Fetch from R2
	tsData, err := r2s.fetchTimeSeriesDataFromR2(accountID, projectID, "heatmaps", startDate, endDate)
	if err != nil {
		return nil, err
	}

	var heatmaps []HeatmapData
	for _, ts := range tsData {
		if heatmapInterface, ok := ts.Data["heatmap"]; ok {
			// Convert back to HeatmapData
			heatmapJSON, _ := json.Marshal(heatmapInterface)
			var heatmap HeatmapData
			if json.Unmarshal(heatmapJSON, &heatmap) == nil {
				if pageURL == "" || heatmap.PageURL == pageURL {
					heatmaps = append(heatmaps, heatmap)
				}
			}
		}
	}

	// Cache results for 10 minutes
	r2s.setCachedData(cacheKey, heatmaps, 10*time.Minute, r2s.heatmapCache)

	return heatmaps, nil
}

// Session Methods

// storeSessionData stores session analytics data to R2
func (r2s *R2AnalyticsService) storeSessionData(accountID, projectID string, sessionData SessionData) error {
	tsData := TimeSeriesData{
		ID:        sessionData.SessionID,
		Timestamp: sessionData.StartTime,
		ProjectID: projectID,
		AccountID: accountID,
		DataType:  "sessions",
		Data: map[string]interface{}{
			"session": sessionData,
		},
		Version:   "1.0",
		CreatedAt: time.Now().UTC(),
	}

	return r2s.storeTimeSeriesDataToR2(tsData)
}

// getSessionData retrieves session data from R2
func (r2s *R2AnalyticsService) getSessionData(accountID, projectID, sessionID string) (*SessionData, error) {
	// Check cache first
	cacheKey := r2s.generateCacheKey("session", accountID, projectID, sessionID)
	if cachedData, found := r2s.getCachedData(cacheKey, r2s.sessionCache); found {
		session := cachedData.(SessionData)
		return &session, nil
	}

	// For specific session ID, we need to search across date ranges
	// In production, you might maintain a session ID -> date mapping in metadata
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02") // Search last 30 days

	tsData, err := r2s.fetchTimeSeriesDataFromR2(accountID, projectID, "sessions", startDate, endDate)
	if err != nil {
		return nil, err
	}

	for _, ts := range tsData {
		if ts.ID == sessionID {
			if sessionInterface, ok := ts.Data["session"]; ok {
				sessionJSON, _ := json.Marshal(sessionInterface)
				var session SessionData
				if json.Unmarshal(sessionJSON, &session) == nil {
					// Cache for 30 minutes
					r2s.setCachedData(cacheKey, session, 30*time.Minute, r2s.sessionCache)
					return &session, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("session %s not found", sessionID)
}

// Recording Methods

// storeSessionRecording stores session recording data to R2
func (r2s *R2AnalyticsService) storeSessionRecording(accountID, projectID, sessionID string, recordingData []map[string]interface{}) error {
	recordingID := uuid.New().String()

	tsData := TimeSeriesData{
		ID:        recordingID,
		Timestamp: time.Now().UTC(),
		ProjectID: projectID,
		AccountID: accountID,
		DataType:  "recordings",
		Data: map[string]interface{}{
			"session_id":  sessionID,
			"events":      recordingData,
			"event_count": len(recordingData),
		},
		Version:   "1.0",
		CreatedAt: time.Now().UTC(),
	}

	return r2s.storeTimeSeriesDataToR2(tsData)
}

// getSessionRecording retrieves session recording from R2
func (r2s *R2AnalyticsService) getSessionRecording(accountID, projectID, sessionID string) ([]map[string]interface{}, error) {
	// Check cache first
	cacheKey := r2s.generateCacheKey("recording", accountID, projectID, sessionID)
	if cachedData, found := r2s.getCachedData(cacheKey, r2s.recordingCache); found {
		return cachedData.([]map[string]interface{}), nil
	}

	// Search for recording by session ID
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	tsData, err := r2s.fetchTimeSeriesDataFromR2(accountID, projectID, "recordings", startDate, endDate)
	if err != nil {
		return nil, err
	}

	for _, ts := range tsData {
		if sessionIDInterface, ok := ts.Data["session_id"]; ok {
			if sessionIDStr, ok := sessionIDInterface.(string); ok && sessionIDStr == sessionID {
				if eventsInterface, ok := ts.Data["events"]; ok {
					eventsJSON, _ := json.Marshal(eventsInterface)
					var events []map[string]interface{}
					if json.Unmarshal(eventsJSON, &events) == nil {
						// Cache for 1 hour (recordings are static)
						r2s.setCachedData(cacheKey, events, 60*time.Minute, r2s.recordingCache)
						return events, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("recording for session %s not found", sessionID)
}

// Cache Management (reuse from original service)

func (r2s *R2AnalyticsService) generateCacheKey(cacheType string, params ...string) string {
	key := cacheType
	for _, param := range params {
		key += ":" + param
	}
	return key
}

func (r2s *R2AnalyticsService) getCachedData(cacheKey string, cacheMap map[string]*CacheEntry) (interface{}, bool) {
	r2s.dataCacheMutex.RLock()
	defer r2s.dataCacheMutex.RUnlock()

	entry, exists := cacheMap[cacheKey]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Data, true
}

func (r2s *R2AnalyticsService) setCachedData(cacheKey string, data interface{}, ttl time.Duration, cacheMap map[string]*CacheEntry) {
	r2s.dataCacheMutex.Lock()
	defer r2s.dataCacheMutex.Unlock()

	cacheMap[cacheKey] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Background Processing

func (r2s *R2AnalyticsService) startBatchProcessor() {
	for {
		select {
		case <-r2s.flushTicker.C:
			r2s.flushEventCache()
		case <-r2s.stopChan:
			r2s.flushTicker.Stop()
			r2s.flushEventCache()
			return
		}
	}
}

func (r2s *R2AnalyticsService) flushEventCache() {
	r2s.cacheMutex.Lock()
	if len(r2s.eventCache) == 0 {
		r2s.cacheMutex.Unlock()
		return
	}

	eventsToFlush := make([]Event, len(r2s.eventCache))
	copy(eventsToFlush, r2s.eventCache)
	r2s.eventCache = r2s.eventCache[:0]
	r2s.cacheMutex.Unlock()

	log.Printf("Flushing %d events from cache to R2", len(eventsToFlush))

	// Group events by account/project/date
	eventGroups := make(map[string][]Event)
	for _, event := range eventsToFlush {
		key := fmt.Sprintf("%s/%s/%s",
			event.AccountID,
			event.ProjectID,
			event.Timestamp.Format("2006-01-02"))
		eventGroups[key] = append(eventGroups[key], event)
	}

	// Store each group as time-series data
	for groupKey, events := range eventGroups {
		batchID := uuid.New().String()

		tsData := TimeSeriesData{
			ID:        batchID,
			Timestamp: events[0].Timestamp,
			ProjectID: events[0].ProjectID,
			AccountID: events[0].AccountID,
			DataType:  "events",
			Data: map[string]interface{}{
				"events":     events,
				"batch_size": len(events),
				"group_key":  groupKey,
			},
			Version:   "1.0",
			CreatedAt: time.Now().UTC(),
		}

		if err := r2s.storeTimeSeriesDataToR2(tsData); err != nil {
			log.Printf("Failed to store event batch %s: %v", batchID, err)
		} else {
			log.Printf("Stored event batch %s with %d events", batchID, len(events))
		}
	}
}

func (r2s *R2AnalyticsService) startCacheCleanup() {
	cleanupTicker := time.NewTicker(10 * time.Minute)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-cleanupTicker.C:
			r2s.cleanExpiredCache()
		case <-r2s.stopChan:
			return
		}
	}
}

func (r2s *R2AnalyticsService) cleanExpiredCache() {
	r2s.dataCacheMutex.Lock()
	defer r2s.dataCacheMutex.Unlock()

	now := time.Now()
	cleaned := 0

	caches := []map[string]*CacheEntry{
		r2s.eventsCache,
		r2s.heatmapCache,
		r2s.sessionCache,
		r2s.recordingCache,
		r2s.metricsCache,
		r2s.dashboardCache,
	}

	for _, cache := range caches {
		for key, entry := range cache {
			if now.After(entry.ExpiresAt) {
				delete(cache, key)
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		log.Printf("R2 Analytics cache cleanup: removed %d expired entries", cleaned)
	}
}

func (r2s *R2AnalyticsService) Stop() {
	close(r2s.stopChan)
}

// HTTP Handlers using R2 backend

func (r2s *R2AnalyticsService) IngestEventHandler(c *gin.Context) {
	var event Event
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract context
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	event.AccountID = accountID.(string)
	event.ProjectID = projectID.(string)

	if event.EventID == "" {
		event.EventID = uuid.New().String()
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Process with R2 backend
	if err := r2s.ingestEventWithR2(event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process event"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"event_id": event.EventID,
		"message":  "Event queued for R2 processing",
	})
}

func (r2s *R2AnalyticsService) GetHeatmapHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	pageURL := c.Query("page_url")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	heatmaps, err := r2s.getHeatmapDataForPage(accountID.(string), projectID.(string), pageURL, startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch heatmap data"})
		return
	}

	// Aggregate heatmap data
	aggregated := r2s.aggregateHeatmapData(heatmaps)

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   aggregated,
		"meta": gin.H{
			"total_heatmaps": len(heatmaps),
			"date_range":     fmt.Sprintf("%s to %s", startDate, endDate),
			"page_url":       pageURL,
		},
	})
}

func (r2s *R2AnalyticsService) aggregateHeatmapData(heatmaps []HeatmapData) map[string]interface{} {
	if len(heatmaps) == 0 {
		return map[string]interface{}{
			"clicks":         []ClickData{},
			"scroll_depth":   map[string]float64{},
			"total_sessions": 0,
		}
	}

	// Aggregate click data
	clickMap := make(map[string]*ClickData)
	totalSessions := len(heatmaps)

	for _, heatmap := range heatmaps {
		for _, click := range heatmap.Clicks {
			key := fmt.Sprintf("%d,%d", click.X, click.Y)
			if existing, ok := clickMap[key]; ok {
				existing.Count += click.Count
			} else {
				clickMap[key] = &ClickData{
					X:     click.X,
					Y:     click.Y,
					Count: click.Count,
				}
			}
		}
	}

	// Convert to slice
	var aggregatedClicks []ClickData
	for _, click := range clickMap {
		aggregatedClicks = append(aggregatedClicks, *click)
	}

	// Sort by count
	sort.Slice(aggregatedClicks, func(i, j int) bool {
		return aggregatedClicks[i].Count > aggregatedClicks[j].Count
	})

	return map[string]interface{}{
		"clicks":         aggregatedClicks,
		"scroll_depth":   map[string]float64{"25%": 0.8, "50%": 0.6, "75%": 0.4, "100%": 0.2},
		"total_sessions": totalSessions,
	}
}
