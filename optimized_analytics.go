package main

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Analytics Event - Time-series optimized structure
type AnalyticsEvent struct {
	ID           uint            `json:"id" gorm:"primaryKey"`
	Time         time.Time       `json:"time" gorm:"index:idx_time_project"`
	ProjectID    string          `json:"project_id" gorm:"index:idx_time_project;index:idx_project_event"`
	UserID       *string         `json:"user_id,omitempty" gorm:"index"`
	SessionID    *string         `json:"session_id,omitempty" gorm:"index"`
	EventType    string          `json:"event_type" gorm:"index:idx_project_event"`
	Properties   json.RawMessage `json:"properties" gorm:"type:jsonb"`
	DeviceInfo   json.RawMessage `json:"device_info,omitempty" gorm:"type:jsonb"`
	LocationInfo json.RawMessage `json:"location_info,omitempty" gorm:"type:jsonb"`
}

// Pre-aggregated daily metrics for fast dashboard queries
type DailyAnalytics struct {
	Date               time.Time       `json:"date" gorm:"primaryKey"`
	ProjectID          string          `json:"project_id" gorm:"primaryKey"`
	TotalUsers         int             `json:"total_users"`
	ActiveUsers        int             `json:"active_users"`
	SessionCount       int             `json:"session_count"`
	AvgSessionDuration int             `json:"avg_session_duration"` // seconds
	BounceRate         float64         `json:"bounce_rate"`
	ConversionRate     float64         `json:"conversion_rate"`
	Revenue            float64         `json:"revenue"`
	TopPages           json.RawMessage `json:"top_pages" gorm:"type:jsonb"`
	DeviceBreakdown    json.RawMessage `json:"device_breakdown" gorm:"type:jsonb"`
	LocationBreakdown  json.RawMessage `json:"location_breakdown" gorm:"type:jsonb"`
	FeatureUsage       json.RawMessage `json:"feature_usage" gorm:"type:jsonb"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// Hourly metrics for real-time analytics
type HourlyAnalytics struct {
	Hour         time.Time `json:"hour" gorm:"primaryKey"`
	ProjectID    string    `json:"project_id" gorm:"primaryKey"`
	ActiveUsers  int       `json:"active_users"`
	SessionCount int       `json:"session_count"`
	PageViews    int       `json:"page_views"`
	Events       int       `json:"events"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Enhanced Analytics Service with caching
type EnhancedAnalyticsServiceV2 struct {
	DB *gorm.DB
	// RedisClient would be added when Redis integration is implemented
}

type EventBatch struct {
	Events    []AnalyticsEvent `json:"events"`
	BatchSize int              `json:"batch_size"`
	Timestamp time.Time        `json:"timestamp"`
}

// Real-time metrics structure for Redis
type RealTimeMetrics struct {
	ProjectID    string    `json:"project_id"`
	ActiveUsers  int       `json:"active_users"`
	SessionCount int       `json:"session_count"`
	PageViews    int       `json:"page_views"`
	Events       int       `json:"events"`
	LastUpdated  time.Time `json:"last_updated"`
}

func NewEnhancedAnalyticsServiceV2(db *gorm.DB) *EnhancedAnalyticsServiceV2 {
	return &EnhancedAnalyticsServiceV2{
		DB: db,
	}
}

// Dashboard metrics response structure
type DashboardMetrics struct {
	TotalUsers         int     `json:"total_users"`
	ActiveUsers        int     `json:"active_users"`
	SessionCount       int     `json:"session_count"`
	AvgSessionDuration float64 `json:"avg_session_duration"`
	BounceRate         float64 `json:"bounce_rate"`
	ConversionRate     float64 `json:"conversion_rate"`
	Revenue            float64 `json:"revenue"`
	ChurnRate          float64 `json:"churn_rate"`
}

// Event patterns for different analytics types
const (
	EventTypePageView     = "page_view"
	EventTypeClick        = "click"
	EventTypeFormSubmit   = "form_submit"
	EventTypeConversion   = "conversion"
	EventTypeFeatureUse   = "feature_use"
	EventTypeSessionStart = "session_start"
	EventTypeSessionEnd   = "session_end"
	EventTypeError        = "error"
)

// Cache keys for Redis
const (
	CacheKeyActiveUsers  = "analytics:%s:active_users:%s" // project_id, timeframe
	CacheKeySessionCount = "analytics:%s:sessions:%s"     // project_id, timeframe
	CacheKeyPageViews    = "analytics:%s:page_views:%s"   // project_id, timeframe
	CacheKeyConversions  = "analytics:%s:conversions:%s"  // project_id, timeframe
	CacheKeyDashboard    = "analytics:%s:dashboard"       // project_id
)

// Batch processing for high-volume event ingestion
func (s *EnhancedAnalyticsServiceV2) ProcessEventBatch(batch EventBatch) error {
	// Use database transaction for batch insert
	tx := s.DB.Begin()

	for _, event := range batch.Events {
		if err := tx.Create(&event).Error; err != nil {
			tx.Rollback()
			return err
		}

		// Update real-time counters in Redis
		s.updateRealTimeMetrics(event)
	}

	return tx.Commit().Error
}

// Real-time metrics update
func (s *EnhancedAnalyticsServiceV2) updateRealTimeMetrics(event AnalyticsEvent) {
	// Implementation would use Redis INCR, ZADD, etc.
	// for real-time counter updates
}

// Get dashboard data with caching
func (s *EnhancedAnalyticsServiceV2) GetDashboardMetrics(projectID string) (*DashboardMetrics, error) {
	// Future: Try cache first with Redis
	// cacheKey := fmt.Sprintf(CacheKeyDashboard, projectID)

	metrics := &DashboardMetrics{}

	// Query pre-aggregated daily analytics
	var dailyMetrics DailyAnalytics
	if err := s.DB.Where("project_id = ? AND date = ?", projectID, time.Now().Truncate(24*time.Hour)).First(&dailyMetrics).Error; err != nil {
		// If no pre-aggregated data, compute on-demand
		return s.computeRealTimeMetrics(projectID)
	}

	// Convert daily metrics to dashboard format
	metrics.TotalUsers = dailyMetrics.TotalUsers
	metrics.ActiveUsers = dailyMetrics.ActiveUsers
	metrics.SessionCount = dailyMetrics.SessionCount
	metrics.AvgSessionDuration = float64(dailyMetrics.AvgSessionDuration)
	metrics.BounceRate = dailyMetrics.BounceRate
	metrics.ConversionRate = dailyMetrics.ConversionRate
	metrics.Revenue = dailyMetrics.Revenue

	// Cache for 5 minutes
	// s.RedisClient.Set(ctx, cacheKey, metricsJSON, 5*time.Minute)

	return metrics, nil
}

// Compute metrics in real-time for recent data
func (s *EnhancedAnalyticsServiceV2) computeRealTimeMetrics(projectID string) (*DashboardMetrics, error) {
	// Complex aggregation queries for real-time calculation
	// This would be expensive and used only as fallback
	return &DashboardMetrics{}, nil
}

// Background job to pre-aggregate data
func (s *EnhancedAnalyticsServiceV2) DailyAggregationJob() {
	// Run daily to pre-compute analytics
	// This would process raw events into DailyAnalytics records
}

// Migration for time-series optimization
func CreateAnalyticsIndices(db *gorm.DB) error {
	// Create time-series optimized indices
	indices := []string{
		"CREATE INDEX CONCURRENTLY idx_analytics_events_time_project ON analytics_events(time DESC, project_id)",
		"CREATE INDEX CONCURRENTLY idx_analytics_events_project_event ON analytics_events(project_id, event_type, time DESC)",
		"CREATE INDEX CONCURRENTLY idx_analytics_events_user_session ON analytics_events(user_id, session_id, time DESC)",

		// Partial indices for better performance
		"CREATE INDEX CONCURRENTLY idx_analytics_events_conversions ON analytics_events(project_id, time DESC) WHERE event_type = 'conversion'",
		"CREATE INDEX CONCURRENTLY idx_analytics_events_page_views ON analytics_events(project_id, time DESC) WHERE event_type = 'page_view'",
	}

	for _, query := range indices {
		if err := db.Exec(query).Error; err != nil {
			return err
		}
	}

	return nil
}
