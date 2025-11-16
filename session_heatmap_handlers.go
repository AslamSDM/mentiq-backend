package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SessionRecordingHandlers provides session recording functionality using R2
type SessionRecordingHandlers struct {
	r2Service *R2AnalyticsService
}

// NewSessionRecordingHandlers creates handlers for session recording
func NewSessionRecordingHandlers(r2Service *R2AnalyticsService) *SessionRecordingHandlers {
	return &SessionRecordingHandlers{
		r2Service: r2Service,
	}
}

// Session Recording Endpoints

func (srh *SessionRecordingHandlers) StartSessionHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	var sessionStart struct {
		UserID     string                 `json:"user_id,omitempty"`
		UserAgent  string                 `json:"user_agent"`
		URL        string                 `json:"url"`
		Viewport   ViewportInfo           `json:"viewport"`
		Metadata   map[string]interface{} `json:"metadata,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&sessionStart); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid session start data"})
		return
	}
	
	sessionID := uuid.New().String()
	
	// Create session data
	sessionData := SessionData{
		SessionID:  sessionID,
		UserID:     sessionStart.UserID,
		StartTime:  time.Now().UTC(),
		IsActive:   true,
		PageViews: []PageView{
			{
				URL:       sessionStart.URL,
				Timestamp: time.Now().UTC(),
			},
		},
		Events: []SessionEvent{},
		DeviceInfo: DeviceInfo{
			UserAgent: sessionStart.UserAgent,
			Viewport:  sessionStart.Viewport,
		},
	}
	
	// Store session to R2
	if err := srh.r2Service.storeSessionData(accountID.(string), projectID.(string), sessionData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start session"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":     "success",
		"session_id": sessionID,
		"message":    "Session started",
	})
}

func (srh *SessionRecordingHandlers) RecordEventHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	sessionID := c.Param("session_id")
	
	var eventData map[string]interface{}
	if err := c.ShouldBindJSON(&eventData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid event data"})
		return
	}
	
	// Add timestamp and session info to event
	eventData["timestamp"] = time.Now().UTC()
	eventData["session_id"] = sessionID
	
	// Store recording event to R2
	recordingData := []map[string]interface{}{eventData}
	
	if err := srh.r2Service.storeSessionRecording(accountID.(string), projectID.(string), sessionID, recordingData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record event"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Event recorded",
	})
}

func (srh *SessionRecordingHandlers) RecordBatchHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	sessionID := c.Param("session_id")
	
	var batchData struct {
		Events []map[string]interface{} `json:"events"`
	}
	
	if err := c.ShouldBindJSON(&batchData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid batch data"})
		return
	}
	
	// Add timestamp and session info to each event
	now := time.Now().UTC()
	for i := range batchData.Events {
		batchData.Events[i]["timestamp"] = now
		batchData.Events[i]["session_id"] = sessionID
		batchData.Events[i]["batch_index"] = i
	}
	
	// Store batch to R2
	if err := srh.r2Service.storeSessionRecording(accountID.(string), projectID.(string), sessionID, batchData.Events); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record batch"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":      "success",
		"events_recorded": len(batchData.Events),
		"message":     "Batch recorded",
	})
}

func (srh *SessionRecordingHandlers) EndSessionHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	sessionID := c.Param("session_id")
	
	var sessionEnd struct {
		Duration int64                  `json:"duration"` // milliseconds
		Events   []SessionEvent        `json:"events,omitempty"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&sessionEnd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid session end data"})
		return
	}
	
	// Get existing session
	session, err := srh.r2Service.getSessionData(accountID.(string), projectID.(string), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}
	
	// Update session with end data
	session.EndTime = time.Now().UTC()
	session.Duration = sessionEnd.Duration / 1000 // convert to seconds
	session.IsActive = false
	
	if len(sessionEnd.Events) > 0 {
		session.Events = append(session.Events, sessionEnd.Events...)
	}
	
	// Store updated session
	if err := srh.r2Service.storeSessionData(accountID.(string), projectID.(string), *session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to end session"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"duration": session.Duration,
		"message":  "Session ended",
	})
}

func (srh *SessionRecordingHandlers) GetSessionHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	sessionID := c.Param("session_id")
	
	session, err := srh.r2Service.getSessionData(accountID.(string), projectID.(string), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   session,
	})
}

func (srh *SessionRecordingHandlers) GetSessionRecordingHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	sessionID := c.Param("session_id")
	
	recording, err := srh.r2Service.getSessionRecording(accountID.(string), projectID.(string), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recording not found"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"session_id":  sessionID,
			"events":      recording,
			"event_count": len(recording),
		},
	})
}

func (srh *SessionRecordingHandlers) ListSessionsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	
	// Check cache first
	cacheKey := fmt.Sprintf("sessions_list:%s:%s:%s:%s", accountID.(string), projectID.(string), startDate, endDate)
	if cachedData, found := srh.r2Service.getCachedData(cacheKey, srh.r2Service.sessionCache); found {
		c.JSON(http.StatusOK, cachedData)
		return
	}
	
	// Fetch sessions from R2
	tsData, err := srh.r2Service.fetchTimeSeriesDataFromR2(accountID.(string), projectID.(string), "sessions", startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch sessions"})
		return
	}
	
	sessions := make([]map[string]interface{}, 0)
	for _, ts := range tsData {
		if sessionInterface, ok := ts.Data["session"]; ok {
			sessionJSON, _ := json.Marshal(sessionInterface)
			var session SessionData
			if json.Unmarshal(sessionJSON, &session) == nil {
				sessions = append(sessions, map[string]interface{}{
					"session_id": session.SessionID,
					"user_id":    session.UserID,
					"start_time": session.StartTime,
					"end_time":   session.EndTime,
					"duration":   session.Duration,
					"page_views": len(session.PageViews),
					"events":     len(session.Events),
					"is_active":  session.IsActive,
				})
			}
		}
	}
	
	response := gin.H{
		"status": "success",
		"data":   sessions,
		"meta": gin.H{
			"total_sessions": len(sessions),
			"date_range":     fmt.Sprintf("%s to %s", startDate, endDate),
		},
	}
	
	// Cache for 5 minutes
	srh.r2Service.setCachedData(cacheKey, response, 5*time.Minute, srh.r2Service.sessionCache)
	
	c.JSON(http.StatusOK, response)
}

// HeatmapHandlers provides heatmap functionality using R2
type HeatmapHandlers struct {
	r2Service *R2AnalyticsService
}

// NewHeatmapHandlers creates handlers for heatmaps
func NewHeatmapHandlers(r2Service *R2AnalyticsService) *HeatmapHandlers {
	return &HeatmapHandlers{
		r2Service: r2Service,
	}
}

// Heatmap Endpoints

func (hh *HeatmapHandlers) RecordHeatmapDataHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	var heatmapData HeatmapData
	if err := c.ShouldBindJSON(&heatmapData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid heatmap data"})
		return
	}
	
	// Add timestamps to click data if missing
	for i := range heatmapData.Clicks {
		if heatmapData.Clicks[i].Timestamp.IsZero() {
			heatmapData.Clicks[i].Timestamp = time.Now().UTC()
		}
	}
	
	for i := range heatmapData.Scrolls {
		if heatmapData.Scrolls[i].Timestamp.IsZero() {
			heatmapData.Scrolls[i].Timestamp = time.Now().UTC()
		}
	}
	
	// Store to R2
	if err := hh.r2Service.storeHeatmapData(accountID.(string), projectID.(string), heatmapData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store heatmap data"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Heatmap data recorded",
		"data": gin.H{
			"page_url":    heatmapData.PageURL,
			"clicks":      len(heatmapData.Clicks),
			"scrolls":     len(heatmapData.Scrolls),
			"hovers":      len(heatmapData.Hovers),
		},
	})
}

func (hh *HeatmapHandlers) RecordClickHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	var clickEvent struct {
		PageURL string    `json:"page_url"`
		X       int       `json:"x"`
		Y       int       `json:"y"`
		Element string    `json:"element,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&clickEvent); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid click data"})
		return
	}
	
	heatmapData := HeatmapData{
		PageURL: clickEvent.PageURL,
		Clicks: []ClickData{
			{
				X:         clickEvent.X,
				Y:         clickEvent.Y,
				Timestamp: time.Now().UTC(),
				Element:   clickEvent.Element,
				Count:     1,
			},
		},
		Scrolls:      []ScrollData{},
		Hovers:       []HoverData{},
		ViewportData: ViewportInfo{},
	}
	
	if err := hh.r2Service.storeHeatmapData(accountID.(string), projectID.(string), heatmapData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record click"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Click recorded",
	})
}

func (hh *HeatmapHandlers) RecordScrollHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	var scrollEvent struct {
		PageURL  string  `json:"page_url"`
		Depth    float64 `json:"depth"`
		MaxDepth float64 `json:"max_depth"`
	}
	
	if err := c.ShouldBindJSON(&scrollEvent); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scroll data"})
		return
	}
	
	heatmapData := HeatmapData{
		PageURL: scrollEvent.PageURL,
		Clicks:  []ClickData{},
		Scrolls: []ScrollData{
			{
				Depth:     scrollEvent.Depth,
				Timestamp: time.Now().UTC(),
				MaxDepth:  scrollEvent.MaxDepth,
			},
		},
		Hovers:       []HoverData{},
		ViewportData: ViewportInfo{},
	}
	
	if err := hh.r2Service.storeHeatmapData(accountID.(string), projectID.(string), heatmapData); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to record scroll"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Scroll recorded",
	})
}

func (hh *HeatmapHandlers) GetPageHeatmapHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	pageURL := c.Query("page_url")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	
	if pageURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page_url parameter is required"})
		return
	}
	
	heatmaps, err := hh.r2Service.getHeatmapDataForPage(accountID.(string), projectID.(string), pageURL, startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch heatmap data"})
		return
	}
	
	// Aggregate heatmap data
	aggregated := hh.aggregateHeatmapData(heatmaps)
	
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data":   aggregated,
		"meta": gin.H{
			"page_url":       pageURL,
			"total_sessions": len(heatmaps),
			"date_range":     fmt.Sprintf("%s to %s", startDate, endDate),
		},
	})
}

func (hh *HeatmapHandlers) GetAllPagesHeatmapHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	
	// Check cache first
	cacheKey := fmt.Sprintf("all_heatmaps:%s:%s:%s:%s", accountID.(string), projectID.(string), startDate, endDate)
	if cachedData, found := hh.r2Service.getCachedData(cacheKey, hh.r2Service.heatmapCache); found {
		c.JSON(http.StatusOK, cachedData)
		return
	}
	
	heatmaps, err := hh.r2Service.getHeatmapDataForPage(accountID.(string), projectID.(string), "", startDate, endDate)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch heatmap data"})
		return
	}
	
	// Group by page URL
	pageHeatmaps := make(map[string][]HeatmapData)
	for _, heatmap := range heatmaps {
		pageHeatmaps[heatmap.PageURL] = append(pageHeatmaps[heatmap.PageURL], heatmap)
	}
	
	// Aggregate data for each page
	pageStats := make(map[string]interface{})
	for pageURL, pageData := range pageHeatmaps {
		pageStats[pageURL] = hh.aggregateHeatmapData(pageData)
	}
	
	response := gin.H{
		"status": "success",
		"data":   pageStats,
		"meta": gin.H{
			"total_pages":    len(pageStats),
			"total_sessions": len(heatmaps),
			"date_range":     fmt.Sprintf("%s to %s", startDate, endDate),
		},
	}
	
	// Cache for 10 minutes
	hh.r2Service.setCachedData(cacheKey, response, 10*time.Minute, hh.r2Service.heatmapCache)
	
	c.JSON(http.StatusOK, response)
}

func (hh *HeatmapHandlers) aggregateHeatmapData(heatmaps []HeatmapData) map[string]interface{} {
	if len(heatmaps) == 0 {
		return map[string]interface{}{
			"clicks":        []ClickData{},
			"scroll_depths": []float64{},
			"total_clicks":  0,
			"total_scrolls": 0,
		}
	}
	
	// Aggregate click data with proximity grouping
	clickMap := make(map[string]*ClickData)
	scrollDepths := []float64{}
	totalClicks := 0
	totalScrolls := 0
	
	for _, heatmap := range heatmaps {
		for _, click := range heatmap.Clicks {
			// Group clicks within 10px radius
			key := fmt.Sprintf("%d_%d", (click.X/10)*10, (click.Y/10)*10)
			if existing, ok := clickMap[key]; ok {
				existing.Count += click.Count
			} else {
				clickMap[key] = &ClickData{
					X:     (click.X / 10) * 10, // Round to nearest 10px
					Y:     (click.Y / 10) * 10,
					Count: click.Count,
				}
			}
			totalClicks += click.Count
		}
		
		for _, scroll := range heatmap.Scrolls {
			scrollDepths = append(scrollDepths, scroll.Depth)
			totalScrolls++
		}
	}
	
	// Convert click map to sorted slice
	var aggregatedClicks []ClickData
	for _, click := range clickMap {
		aggregatedClicks = append(aggregatedClicks, *click)
	}
	
	// Sort clicks by count (most clicked first)
	for i := 0; i < len(aggregatedClicks)-1; i++ {
		for j := i + 1; j < len(aggregatedClicks); j++ {
			if aggregatedClicks[i].Count < aggregatedClicks[j].Count {
				aggregatedClicks[i], aggregatedClicks[j] = aggregatedClicks[j], aggregatedClicks[i]
			}
		}
	}
	
	return map[string]interface{}{
		"clicks":        aggregatedClicks,
		"scroll_depths": scrollDepths,
		"total_clicks":  totalClicks,
		"total_scrolls": totalScrolls,
		"sessions":      len(heatmaps),
	}
}