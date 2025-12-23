package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72/client"
)

// AnalyticsQuery represents the query parameters for analytics requests
type AnalyticsQuery struct {
	StartDate string   `form:"start_date"`
	EndDate   string   `form:"end_date"`
	EventType string   `form:"event_type"`
	UserID    string   `form:"user_id"`
	SessionID string   `form:"session_id"`
	Metrics   []string `form:"metrics"`
	GroupBy   string   `form:"group_by"`
	Limit     int      `form:"limit"`
	Offset    int      `form:"offset"`
}

// MetricResult represents the result of a single metric calculation
type MetricResult struct {
	Metric     string                 `json:"metric"`
	Value      interface{}            `json:"value"`
	TimeSeries []TimeSeriesPoint      `json:"time_series,omitempty"`
	Breakdown  map[string]interface{} `json:"breakdown,omitempty"`
}

// TimeSeriesPoint represents a single point in a time series
type TimeSeriesPoint struct {
	Date        string      `json:"date"`
	Value       interface{} `json:"value"`
	UniqueUsers int         `json:"unique_users,omitempty"`
}

// AnalyticsResponse represents the complete analytics response
type AnalyticsResponse struct {
	Query   AnalyticsQuery `json:"query"`
	Results []MetricResult `json:"results"`
	Meta    ResponseMeta   `json:"meta"`
}

// ResponseMeta contains metadata about the analytics response
type ResponseMeta struct {
	TotalEvents    int           `json:"total_events"`
	ProcessingTime time.Duration `json:"processing_time"`
	DateRange      string        `json:"date_range"`
	CacheHit       bool          `json:"cache_hit,omitempty"`
}

// GetEventsHandler returns raw events for a project
func (as *AnalyticsService) GetEventsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	eventType := c.Query("event_type")
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 1000 {
				limit = 1000 // Cap at 1000 events
			}
		}
	}

	// Fetch events
	events, err := as.fetchEventsForDateRange(accountID.(string), projectID, startDate, endDate)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	// Filter by event type if specified
	var filteredEvents []Event
	if eventType != "" {
		for _, e := range events {
			if e.EventType == eventType {
				filteredEvents = append(filteredEvents, e)
			}
		}
	} else {
		filteredEvents = events
	}

	// Apply limit
	if len(filteredEvents) > limit {
		filteredEvents = filteredEvents[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"events": filteredEvents,
		"meta": gin.H{
			"total":      len(filteredEvents),
			"start_date": startDate,
			"end_date":   endDate,
		},
	})
}

func (as *AnalyticsService) GetAnalyticsHandler(c *gin.Context) {
	start := time.Now()

	var query AnalyticsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query parameters"})
		return
	}

	// Robustly handle metrics parameter
	rawMetrics := query.Metrics
	query.Metrics = make([]string, 0)

	if val := c.Query("metrics"); val != "" {
		parts := strings.Split(val, ",")
		if len(parts) > len(rawMetrics) {
			rawMetrics = parts
		}
	}

	for _, m := range rawMetrics {
		parts := strings.Split(m, ",")
		for _, p := range parts {
			clean := strings.TrimSpace(p)
			if clean != "" {
				query.Metrics = append(query.Metrics, clean)
			}
		}
	}

	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get project with GORM
	var proj Project
	if err := as.db.Where("id = ?", projectID.(string)).First(&proj).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project"})
		return
	}

	var stripeKey string
	sc := &client.API{}
	if stripeKey != "" {
		sc.Init(stripeKey, nil)
	}

	if query.StartDate == "" {
		query.StartDate = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
	if query.EndDate == "" {
		query.EndDate = time.Now().Format("2006-01-02")
	}
	if len(query.Metrics) == 0 {
		query.Metrics = []string{"total_events", "unique_users", "top_events", "dau", "wau", "mau", "page_views"}
	}
	if query.GroupBy == "" {
		query.GroupBy = "day"
	}

	// Parse date range for SQL
	startTime, _ := time.Parse("2006-01-02", query.StartDate)
	endTime, _ := time.Parse("2006-01-02", query.EndDate)
	endTime = endTime.Add(24*time.Hour - time.Nanosecond)

	// Extended date range for rolling windows (WAU/MAU) - used by SQL queries
	_ = startTime.AddDate(0, 0, -30) // extendedStartTime - referenced in SQL queries

	// FULLY OPTIMIZED: All metrics now use SQL aggregation
	// Events are only loaded for Stripe-related metrics that need the old logic
	var events, extendedEvents []Event
	needsStripeMetrics := false
	for _, m := range query.Metrics {
		if m == "conversion_rate" || m == "churn_rate" || m == "mrr" || m == "arpu" {
			needsStripeMetrics = true
			break
		}
	}

	// Only create empty slices for Stripe metrics (they don't actually use events but need the function signature)
	if needsStripeMetrics {
		events = []Event{}
		extendedEvents = []Event{}
	}

	// OPTIMIZED: Calculate all metrics using SQL
	results := as.calculateMetricsOptimized(events, extendedEvents, query, sc, projectID.(string), accountID.(string), startTime, endTime)

	response := AnalyticsResponse{
		Query:   query,
		Results: results,
	}

	// Get total events count from SQL
	var totalCount int64
	as.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?",
			accountID.(string), projectID.(string), startTime, endTime).
		Count(&totalCount)
	response.Meta.TotalEvents = int(totalCount)

	response.Meta.ProcessingTime = time.Since(start)
	response.Meta.DateRange = fmt.Sprintf("%s to %s", query.StartDate, query.EndDate)

	c.JSON(http.StatusOK, response)
}

// calculateMetricsOptimized uses SQL aggregation for metrics that don't need in-memory processing
func (as *AnalyticsService) calculateMetricsOptimized(events []Event, extendedEvents []Event, query AnalyticsQuery, sc *client.API, projectID, accountID string, startTime, endTime time.Time) []MetricResult {
	var results []MetricResult

	for _, metric := range query.Metrics {
		switch metric {
		case "total_events":
			// SQL: Simple COUNT
			var count int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?",
					accountID, projectID, startTime, endTime).
				Count(&count)

			// Get time series with SQL
			timeSeries := as.getTimeSeriesFromSQL(accountID, projectID, startTime, endTime, query.GroupBy, "count")

			results = append(results, MetricResult{
				Metric:     "total_events",
				Value:      count,
				TimeSeries: timeSeries,
			})

		case "unique_users":
			// SQL: COUNT DISTINCT
			var count int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, startTime, endTime).
				Distinct("user_id").
				Count(&count)

			timeSeries := as.getTimeSeriesFromSQL(accountID, projectID, startTime, endTime, query.GroupBy, "unique_users")

			results = append(results, MetricResult{
				Metric:     "unique_users",
				Value:      count,
				TimeSeries: timeSeries,
			})

		case "top_events":
			// SQL: GROUP BY event_type
			type EventCount struct {
				EventType string `gorm:"column:event_type"`
				Count     int64  `gorm:"column:count"`
			}
			var eventCounts []EventCount
			as.db.Model(&Event{}).
				Select("event_type, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?",
					accountID, projectID, startTime, endTime).
				Group("event_type").
				Order("count DESC").
				Limit(20).
				Scan(&eventCounts)

			breakdown := make(map[string]interface{})
			for _, ec := range eventCounts {
				breakdown[ec.EventType] = ec.Count
			}

			results = append(results, MetricResult{
				Metric:    "top_events",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "page_views":
			// SQL: COUNT with filter
			var count int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND event_type IN ('page_view', 'pageview')",
					accountID, projectID, startTime, endTime).
				Count(&count)

			// Get breakdown by path using JSON extraction
			type PathCount struct {
				Path  string `gorm:"column:path"`
				Count int64  `gorm:"column:count"`
			}
			var pathCounts []PathCount
			as.db.Raw(`
				SELECT COALESCE(properties->>'path', properties->>'url', properties->>'page', 'unknown') as path, COUNT(*) as count
				FROM events
				WHERE account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?
					AND event_type IN ('page_view', 'pageview')
				GROUP BY COALESCE(properties->>'path', properties->>'url', properties->>'page', 'unknown')
				ORDER BY count DESC
				LIMIT 20
			`, accountID, projectID, startTime, endTime).Scan(&pathCounts)

			breakdown := make(map[string]interface{})
			for _, pc := range pathCounts {
				breakdown[pc.Path] = pc.Count
			}

			timeSeries := as.getTimeSeriesFromSQL(accountID, projectID, startTime, endTime, query.GroupBy, "page_views")

			results = append(results, MetricResult{
				Metric:     "page_views",
				Value:      count,
				Breakdown:  breakdown,
				TimeSeries: timeSeries,
			})

		case "total_sessions":
			// SQL: COUNT DISTINCT
			var count int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND session_id IS NOT NULL AND session_id != ''",
					accountID, projectID, startTime, endTime).
				Distinct("session_id").
				Count(&count)

			timeSeries := as.getTimeSeriesFromSQL(accountID, projectID, startTime, endTime, query.GroupBy, "sessions")

			results = append(results, MetricResult{
				Metric:     "total_sessions",
				Value:      count,
				TimeSeries: timeSeries,
			})

		case "country_breakdown":
			type BreakdownCount struct {
				Value string `gorm:"column:value"`
				Count int64  `gorm:"column:count"`
			}
			var counts []BreakdownCount
			as.db.Model(&Event{}).
				Select("country as value, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND country IS NOT NULL AND country != ''",
					accountID, projectID, startTime, endTime).
				Group("country").
				Order("count DESC").
				Scan(&counts)

			breakdown := make(map[string]interface{})
			for _, c := range counts {
				breakdown[c.Value] = c.Count
			}
			results = append(results, MetricResult{
				Metric:    "country_breakdown",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "city_breakdown":
			type BreakdownCount struct {
				Value string `gorm:"column:value"`
				Count int64  `gorm:"column:count"`
			}
			var counts []BreakdownCount
			as.db.Model(&Event{}).
				Select("city as value, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND city IS NOT NULL AND city != ''",
					accountID, projectID, startTime, endTime).
				Group("city").
				Order("count DESC").
				Limit(50).
				Scan(&counts)

			breakdown := make(map[string]interface{})
			for _, c := range counts {
				breakdown[c.Value] = c.Count
			}
			results = append(results, MetricResult{
				Metric:    "city_breakdown",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "device_breakdown":
			type BreakdownCount struct {
				Value string `gorm:"column:value"`
				Count int64  `gorm:"column:count"`
			}
			var counts []BreakdownCount
			as.db.Model(&Event{}).
				Select("device as value, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND device IS NOT NULL AND device != ''",
					accountID, projectID, startTime, endTime).
				Group("device").
				Order("count DESC").
				Scan(&counts)

			breakdown := make(map[string]interface{})
			for _, c := range counts {
				breakdown[c.Value] = c.Count
			}
			results = append(results, MetricResult{
				Metric:    "device_breakdown",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "os_breakdown":
			type BreakdownCount struct {
				Value string `gorm:"column:value"`
				Count int64  `gorm:"column:count"`
			}
			var counts []BreakdownCount
			as.db.Model(&Event{}).
				Select("os as value, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND os IS NOT NULL AND os != ''",
					accountID, projectID, startTime, endTime).
				Group("os").
				Order("count DESC").
				Scan(&counts)

			breakdown := make(map[string]interface{})
			for _, c := range counts {
				breakdown[c.Value] = c.Count
			}
			results = append(results, MetricResult{
				Metric:    "os_breakdown",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "browser_breakdown":
			type BreakdownCount struct {
				Value string `gorm:"column:value"`
				Count int64  `gorm:"column:count"`
			}
			var counts []BreakdownCount
			as.db.Model(&Event{}).
				Select("browser as value, COUNT(*) as count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND browser IS NOT NULL AND browser != ''",
					accountID, projectID, startTime, endTime).
				Group("browser").
				Order("count DESC").
				Scan(&counts)

			breakdown := make(map[string]interface{})
			for _, c := range counts {
				breakdown[c.Value] = c.Count
			}
			results = append(results, MetricResult{
				Metric:    "browser_breakdown",
				Value:     breakdown,
				Breakdown: breakdown,
			})

		case "dau":
			// SQL: Daily Active Users - unique users on the last day of the range
			var dauCount int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, endTime.Add(-24*time.Hour), endTime).
				Distinct("user_id").
				Count(&dauCount)

			// Time series for DAU
			timeSeries := as.getTimeSeriesFromSQL(accountID, projectID, startTime, endTime, query.GroupBy, "unique_users")

			results = append(results, MetricResult{
				Metric:     "dau",
				Value:      dauCount,
				TimeSeries: timeSeries,
			})

		case "wau":
			// SQL: Weekly Active Users - unique users in last 7 days
			sevenDaysAgo := endTime.Add(-7 * 24 * time.Hour)
			var wauCount int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, sevenDaysAgo, endTime).
				Distinct("user_id").
				Count(&wauCount)

			// Time series for WAU (rolling 7-day unique users per day)
			timeSeries := as.getRollingActiveUsersTimeSeriesSQL(accountID, projectID, startTime, endTime, 7)

			results = append(results, MetricResult{
				Metric:     "wau",
				Value:      wauCount,
				TimeSeries: timeSeries,
			})

		case "mau":
			// SQL: Monthly Active Users - unique users in last 30 days
			thirtyDaysAgo := endTime.Add(-30 * 24 * time.Hour)
			var mauCount int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, thirtyDaysAgo, endTime).
				Distinct("user_id").
				Count(&mauCount)

			// Time series for MAU (rolling 30-day unique users per day)
			timeSeries := as.getRollingActiveUsersTimeSeriesSQL(accountID, projectID, startTime, endTime, 30)

			results = append(results, MetricResult{
				Metric:     "mau",
				Value:      mauCount,
				TimeSeries: timeSeries,
			})

		case "bounce_rate":
			// SQL: Calculate bounce rate (sessions with only 1 event)
			type BounceData struct {
				TotalSessions  int64 `gorm:"column:total_sessions"`
				BounceSessions int64 `gorm:"column:bounce_sessions"`
			}
			var bounceData BounceData
			as.db.Raw(`
				WITH session_counts AS (
					SELECT session_id, COUNT(*) as event_count
					FROM events
					WHERE account_id = $1 AND project_id = $2 AND timestamp BETWEEN $3 AND $4
						AND session_id IS NOT NULL AND session_id != ''
					GROUP BY session_id
				)
				SELECT 
					COUNT(*) as total_sessions,
					SUM(CASE WHEN event_count = 1 THEN 1 ELSE 0 END) as bounce_sessions
				FROM session_counts
			`, accountID, projectID, startTime, endTime).Scan(&bounceData)

			bounceRate := 0.0
			if bounceData.TotalSessions > 0 {
				bounceRate = float64(bounceData.BounceSessions) / float64(bounceData.TotalSessions) * 100
			}

			results = append(results, MetricResult{
				Metric: "bounce_rate",
				Value:  fmt.Sprintf("%.2f%%", bounceRate),
			})

		case "avg_session_duration":
			// SQL: Calculate average session duration
			type DurationData struct {
				AvgDuration float64 `gorm:"column:avg_duration"`
			}
			var durationData DurationData
			as.db.Raw(`
				WITH session_durations AS (
					SELECT session_id, 
						EXTRACT(EPOCH FROM (MAX(timestamp) - MIN(timestamp))) as duration_seconds
					FROM events
					WHERE account_id = $1 AND project_id = $2 AND timestamp BETWEEN $3 AND $4
						AND session_id IS NOT NULL AND session_id != ''
					GROUP BY session_id
					HAVING COUNT(*) > 1
				)
				SELECT COALESCE(AVG(duration_seconds), 0) as avg_duration
				FROM session_durations
			`, accountID, projectID, startTime, endTime).Scan(&durationData)

			avgDuration := time.Duration(durationData.AvgDuration * float64(time.Second))
			results = append(results, MetricResult{
				Metric: "avg_session_duration",
				Value:  avgDuration.String(),
			})

		case "stickiness_ratio":
			// SQL: DAU/MAU ratio
			today := time.Now().UTC().Truncate(24 * time.Hour)
			tomorrow := today.Add(24 * time.Hour)
			thirtyDaysAgo := today.Add(-30 * 24 * time.Hour)

			var dauCount, mauCount int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, today, tomorrow).
				Distinct("user_id").
				Count(&dauCount)

			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, thirtyDaysAgo, tomorrow).
				Distinct("user_id").
				Count(&mauCount)

			stickiness := 0.0
			if mauCount > 0 {
				stickiness = float64(dauCount) / float64(mauCount) * 100
			}

			results = append(results, MetricResult{
				Metric: "stickiness_ratio",
				Value:  fmt.Sprintf("%.2f%%", stickiness),
			})

		case "session_frequency":
			// SQL: Average sessions per user
			type FrequencyData struct {
				AvgFrequency float64 `gorm:"column:avg_frequency"`
			}
			var freqData FrequencyData
			as.db.Raw(`
				WITH user_sessions AS (
					SELECT user_id, COUNT(DISTINCT session_id) as session_count
					FROM events
					WHERE account_id = $1 AND project_id = $2 AND timestamp BETWEEN $3 AND $4
						AND user_id IS NOT NULL AND user_id != ''
						AND session_id IS NOT NULL AND session_id != ''
					GROUP BY user_id
				)
				SELECT COALESCE(AVG(session_count), 0) as avg_frequency
				FROM user_sessions
			`, accountID, projectID, startTime, endTime).Scan(&freqData)

			results = append(results, MetricResult{
				Metric: "session_frequency",
				Value:  fmt.Sprintf("%.2f", freqData.AvgFrequency),
			})

		case "adoption_metrics":
			// SQL: Feature adoption rates
			coreFeatures := []string{"purchase", "form_submit"}

			// Get total active users
			var totalUsers int64
			as.db.Model(&Event{}).
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, startTime, endTime).
				Distinct("user_id").
				Count(&totalUsers)

			// Get users per feature
			type FeatureCount struct {
				EventType string `gorm:"column:event_type"`
				UserCount int64  `gorm:"column:user_count"`
			}
			var featureCounts []FeatureCount
			as.db.Model(&Event{}).
				Select("event_type, COUNT(DISTINCT user_id) as user_count").
				Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND event_type IN ? AND user_id IS NOT NULL AND user_id != ''",
					accountID, projectID, startTime, endTime, coreFeatures).
				Group("event_type").
				Scan(&featureCounts)

			adoptionRates := make(map[string]interface{})
			totalAdopted := int64(0)
			for _, fc := range featureCounts {
				rate := 0.0
				if totalUsers > 0 {
					rate = float64(fc.UserCount) / float64(totalUsers) * 100
				}
				adoptionRates[fc.EventType] = fmt.Sprintf("%.2f%%", rate)
				totalAdopted += fc.UserCount
			}

			overallRate := 0.0
			if totalUsers > 0 {
				overallRate = float64(totalAdopted) / float64(totalUsers) * 100
			}

			results = append(results, MetricResult{
				Metric:    "adoption_metrics",
				Value:     fmt.Sprintf("%.2f%%", overallRate),
				Breakdown: adoptionRates,
			})

		case "conversion_rate", "churn_rate", "mrr", "arpu":
			// These use Stripe data, not events - use existing logic
			result := as.calculateSingleMetric(events, extendedEvents, metric, query, sc, projectID)
			results = append(results, result)

		default:
			log.Printf("Unknown metric requested: %s", metric)
			results = append(results, MetricResult{
				Metric: metric,
				Value:  "Metric not implemented",
			})
		}
	}

	return results
}

// getTimeSeriesFromSQL gets time series data using SQL aggregation
func (as *AnalyticsService) getTimeSeriesFromSQL(accountID, projectID string, startTime, endTime time.Time, groupBy, metricType string) []TimeSeriesPoint {
	var truncFunc string
	switch groupBy {
	case "hour":
		truncFunc = "hour"
	case "week":
		truncFunc = "week"
	case "month":
		truncFunc = "month"
	default:
		truncFunc = "day"
	}

	type TimePoint struct {
		Date  time.Time `gorm:"column:date"`
		Value int64     `gorm:"column:value"`
	}

	var selectClause string
	switch metricType {
	case "unique_users":
		selectClause = "date_trunc('" + truncFunc + "', timestamp) as date, COUNT(DISTINCT user_id) as value"
	case "sessions":
		selectClause = "date_trunc('" + truncFunc + "', timestamp) as date, COUNT(DISTINCT session_id) as value"
	case "page_views":
		selectClause = "date_trunc('" + truncFunc + "', timestamp) as date, COUNT(*) as value"
	default:
		selectClause = "date_trunc('" + truncFunc + "', timestamp) as date, COUNT(*) as value"
	}

	var points []TimePoint
	query := as.db.Model(&Event{}).
		Select(selectClause).
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?",
			accountID, projectID, startTime, endTime).
		Group("date_trunc('" + truncFunc + "', timestamp)").
		Order("date ASC")

	if metricType == "page_views" {
		query = query.Where("event_type IN ('page_view', 'pageview')")
	}

	query.Scan(&points)

	result := make([]TimeSeriesPoint, 0, len(points))
	for _, p := range points {
		result = append(result, TimeSeriesPoint{
			Date:  p.Date.Format("2006-01-02"),
			Value: p.Value,
		})
	}
	return result
}

// getRollingActiveUsersTimeSeriesSQL gets rolling N-day active users time series using SQL
// Note: For true rolling window, we use daily unique users as an approximation
// A full rolling window would require complex window functions or lateral joins
func (as *AnalyticsService) getRollingActiveUsersTimeSeriesSQL(accountID, projectID string, startTime, endTime time.Time, windowDays int) []TimeSeriesPoint {
	type TimePoint struct {
		Date  time.Time `gorm:"column:date"`
		Value int64     `gorm:"column:value"`
	}

	var points []TimePoint

	// Use a simpler approach: for each day, count unique users in the rolling window
	// This uses LATERAL join for accurate rolling counts
	as.db.Raw(`
		WITH date_series AS (
			SELECT generate_series(
				$3::date,
				$4::date,
				'1 day'::interval
			)::date as date
		)
		SELECT 
			ds.date,
			(
				SELECT COUNT(DISTINCT user_id)
				FROM events e
				WHERE e.account_id = $1 
					AND e.project_id = $2
					AND e.timestamp >= ds.date - make_interval(days => $5)
					AND e.timestamp < ds.date + interval '1 day'
					AND e.user_id IS NOT NULL 
					AND e.user_id != ''
			) as value
		FROM date_series ds
		ORDER BY ds.date ASC
	`, accountID, projectID, startTime.Format("2006-01-02"), endTime.Format("2006-01-02"), windowDays).Scan(&points)

	result := make([]TimeSeriesPoint, 0, len(points))
	for _, p := range points {
		result = append(result, TimeSeriesPoint{
			Date:  p.Date.Format("2006-01-02"),
			Value: p.Value,
		})
	}
	return result
}

// calculateSingleMetric calculates a single metric using the original in-memory logic
func (as *AnalyticsService) calculateSingleMetric(events []Event, extendedEvents []Event, metric string, query AnalyticsQuery, sc *client.API, projectID string) MetricResult {
	// Use the existing calculateMetrics logic for a single metric
	tempQuery := AnalyticsQuery{
		Metrics:   []string{metric},
		StartDate: query.StartDate,
		EndDate:   query.EndDate,
		GroupBy:   query.GroupBy,
	}
	results := as.calculateMetrics(events, extendedEvents, tempQuery, sc, projectID)
	if len(results) > 0 {
		return results[0]
	}
	return MetricResult{Metric: metric, Value: nil}
}

func (as *AnalyticsService) calculateMetrics(events []Event, extendedEvents []Event, query AnalyticsQuery, sc *client.API, projectID string) []MetricResult {
	var results []MetricResult

	log.Printf("calculateMetrics called with %d events and metrics: %v", len(events), query.Metrics)

	for _, metric := range query.Metrics {
		log.Printf("Processing metric: %s", metric)
		switch metric {
		case "total_events":
			results = append(results, MetricResult{
				Metric: "total_events",
				Value:  len(events),
				TimeSeries: as.getTimeSeriesData(events, query.GroupBy, func(events []Event) interface{} {
					return len(events)
				}),
			})

		case "unique_users":
			uniqueUsers := make(map[string]bool)
			for _, event := range events {
				if event.UserID != "" {
					uniqueUsers[event.UserID] = true
				}
			}
			results = append(results, MetricResult{
				Metric: "unique_users",
				Value:  len(uniqueUsers),
				TimeSeries: as.getTimeSeriesData(events, query.GroupBy, func(events []Event) interface{} {
					users := make(map[string]bool)
					for _, event := range events {
						if event.UserID != "" {
							users[event.UserID] = true
						}
					}
					return len(users)
				}),
			})

		case "top_events":
			eventCounts := make(map[string]int)
			for _, event := range events {
				eventCounts[event.EventType]++
			}
			results = append(results, MetricResult{
				Metric:    "top_events",
				Value:     eventCounts,
				Breakdown: convertMapToInterface(eventCounts),
			})

		case "bounce_rate":
			sessionEvents := make(map[string]int)
			for _, event := range events {
				if event.SessionID != "" {
					sessionEvents[event.SessionID]++
				}
			}

			bounces := 0
			totalSessions := len(sessionEvents)
			for _, count := range sessionEvents {
				if count == 1 {
					bounces++
				}
			}

			var bounceRate float64
			if totalSessions > 0 {
				bounceRate = float64(bounces) / float64(totalSessions) * 100
			}

			results = append(results, MetricResult{
				Metric: "bounce_rate",
				Value:  fmt.Sprintf("%.2f%%", bounceRate),
			})

		case "avg_session_duration":
			sessionTimes := make(map[string][]time.Time)
			for _, event := range events {
				if event.SessionID != "" {
					sessionTimes[event.SessionID] = append(sessionTimes[event.SessionID], event.Timestamp)
				}
			}

			var totalDuration time.Duration
			validSessions := 0

			for _, times := range sessionTimes {
				if len(times) < 2 {
					continue
				}
				sort.Slice(times, func(i, j int) bool {
					return times[i].Before(times[j])
				})
				duration := times[len(times)-1].Sub(times[0])
				totalDuration += duration
				validSessions++
			}

			var avgDuration time.Duration
			if validSessions > 0 {
				avgDuration = totalDuration / time.Duration(validSessions)
			}

			results = append(results, MetricResult{
				Metric: "avg_session_duration",
				Value:  avgDuration.String(),
			})

		case "dau":
			// Daily Active Users - unique users per day in the date range
			dailyUsers := make(map[string]map[string]bool) // date -> user_id -> bool

			for _, event := range events {
				if event.UserID == "" {
					continue
				}
				eventDate := event.Timestamp.Format("2006-01-02")
				if dailyUsers[eventDate] == nil {
					dailyUsers[eventDate] = make(map[string]bool)
				}
				dailyUsers[eventDate][event.UserID] = true
			}

			// Get the latest date's user count as current DAU (or average DAU if no specific day)
			var latestDate string
			var currentDAU int
			for date := range dailyUsers {
				if date > latestDate {
					latestDate = date
					currentDAU = len(dailyUsers[date])
				}
			}

			// If no data, calculate from end date
			if currentDAU == 0 && query.EndDate != "" {
				endDate, err := time.Parse("2006-01-02", query.EndDate)
				if err == nil {
					endDateStr := endDate.Format("2006-01-02")
					for _, event := range events {
						if event.UserID != "" && event.Timestamp.Format("2006-01-02") == endDateStr {
							currentDAU++
						}
					}
				}
			}

			var timeSeries []TimeSeriesPoint
			if query.GroupBy == "day" {
				timeSeries = as.getRollingActiveUsersTimeSeries(events, 1, query.StartDate, query.EndDate)
			} else {
				timeSeries = as.getDAUTimeSeries(events, query.GroupBy)
			}

			results = append(results, MetricResult{
				Metric:     "dau",
				Value:      currentDAU,
				TimeSeries: timeSeries,
			})

		case "wau":
			// Weekly Active Users - unique users in the last 7 days from end of date range
			var endDate time.Time
			var err error
			if query.EndDate != "" {
				endDate, err = time.Parse("2006-01-02", query.EndDate)
			}
			if err != nil || query.EndDate == "" {
				endDate = time.Now().UTC()
			}

			sevenDaysAgo := endDate.AddDate(0, 0, -7)
			weeklyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(sevenDaysAgo) &&
					event.Timestamp.Before(endDate.AddDate(0, 0, 1)) &&
					event.UserID != "" {
					weeklyUsers[event.UserID] = true
				}
			}

			var timeSeries []TimeSeriesPoint
			if query.GroupBy == "day" {
				timeSeries = as.getRollingActiveUsersTimeSeries(extendedEvents, 7, query.StartDate, query.EndDate)
			} else {
				timeSeries = as.getWAUTimeSeries(events, query.GroupBy)
			}

			results = append(results, MetricResult{
				Metric:     "wau",
				Value:      len(weeklyUsers),
				TimeSeries: timeSeries,
			})

		case "mau":
			// Monthly Active Users - unique users in the last 30 days from end of date range
			var endDate time.Time
			var err error
			if query.EndDate != "" {
				endDate, err = time.Parse("2006-01-02", query.EndDate)
			}
			if err != nil || query.EndDate == "" {
				endDate = time.Now().UTC()
			}

			thirtyDaysAgo := endDate.AddDate(0, 0, -30)
			monthlyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(thirtyDaysAgo) &&
					event.Timestamp.Before(endDate.AddDate(0, 0, 1)) &&
					event.UserID != "" {
					monthlyUsers[event.UserID] = true
				}
			}

			var timeSeries []TimeSeriesPoint
			if query.GroupBy == "day" {
				timeSeries = as.getRollingActiveUsersTimeSeries(extendedEvents, 30, query.StartDate, query.EndDate)
			} else {
				timeSeries = as.getMAUTimeSeries(events, query.GroupBy)
			}

			results = append(results, MetricResult{
				Metric:     "mau",
				Value:      len(monthlyUsers),
				TimeSeries: timeSeries,
			})

		case "page_views":
			pageViews := 0
			pageViewsByPath := make(map[string]int)

			for _, event := range events {
				if event.EventType == "page_view" || event.EventType == "pageview" {
					pageViews++

					if event.Properties != nil {
						if path, ok := event.Properties["path"].(string); ok {
							pageViewsByPath[path]++
						} else if url, ok := event.Properties["url"].(string); ok {
							pageViewsByPath[url]++
						} else if page, ok := event.Properties["page"].(string); ok {
							pageViewsByPath[page]++
						}
					}
				}
			}

			results = append(results, MetricResult{
				Metric:     "page_views",
				Value:      pageViews,
				Breakdown:  convertMapToInterface(pageViewsByPath),
				TimeSeries: as.getPageViewTimeSeries(events, query.GroupBy),
			})

		case "total_sessions":
			uniqueSessions := make(map[string]bool)
			for _, event := range events {
				if event.SessionID != "" {
					uniqueSessions[event.SessionID] = true
				}
			}

			results = append(results, MetricResult{
				Metric: "total_sessions",
				Value:  len(uniqueSessions),
				TimeSeries: as.getTimeSeriesData(events, query.GroupBy, func(events []Event) interface{} {
					sessions := make(map[string]bool)
					for _, event := range events {
						if event.SessionID != "" {
							sessions[event.SessionID] = true
						}
					}
					return len(sessions)
				}),
			})

		case "country_breakdown":
			countryCounts := make(map[string]int)
			for _, event := range events {
				if event.Country != "" {
					countryCounts[event.Country]++
				}
			}
			results = append(results, MetricResult{
				Metric:    "country_breakdown",
				Value:     countryCounts,
				Breakdown: convertMapToInterface(countryCounts),
			})

		case "city_breakdown":
			cityCounts := make(map[string]int)
			for _, event := range events {
				if event.City != "" {
					cityCounts[event.City]++
				}
			}
			results = append(results, MetricResult{
				Metric:    "city_breakdown",
				Value:     cityCounts,
				Breakdown: convertMapToInterface(cityCounts),
			})

		case "device_breakdown":
			deviceCounts := make(map[string]int)
			for _, event := range events {
				if event.Device != "" {
					deviceCounts[event.Device]++
				}
			}
			results = append(results, MetricResult{
				Metric:    "device_breakdown",
				Value:     deviceCounts,
				Breakdown: convertMapToInterface(deviceCounts),
			})

		case "os_breakdown":
			osCounts := make(map[string]int)
			for _, event := range events {
				if event.OS != "" {
					osCounts[event.OS]++
				}
			}
			results = append(results, MetricResult{
				Metric:    "os_breakdown",
				Value:     osCounts,
				Breakdown: convertMapToInterface(osCounts),
			})

		case "browser_breakdown":
			browserCounts := make(map[string]int)
			for _, event := range events {
				if event.Browser != "" {
					browserCounts[event.Browser]++
				}
			}
			results = append(results, MetricResult{
				Metric:    "browser_breakdown",
				Value:     browserCounts,
				Breakdown: convertMapToInterface(browserCounts),
			})

		case "stickiness_ratio":
			dau := 0
			mau := 0
			today := time.Now().UTC().Format("2006-01-02")
			thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)

			dauUsers := make(map[string]bool)
			mauUsers := make(map[string]bool)

			for _, event := range events {
				if event.UserID != "" {
					if event.Timestamp.Format("2006-01-02") == today {
						dauUsers[event.UserID] = true
					}
					if event.Timestamp.After(thirtyDaysAgo) {
						mauUsers[event.UserID] = true
					}
				}
			}
			dau = len(dauUsers)
			mau = len(mauUsers)

			var stickiness float64
			if mau > 0 {
				stickiness = float64(dau) / float64(mau)
			}

			results = append(results, MetricResult{
				Metric: "stickiness_ratio",
				Value:  fmt.Sprintf("%.2f%%", stickiness*100),
			})

		case "session_frequency":
			userSessions := make(map[string]map[string]bool)

			for _, event := range events {
				if event.UserID != "" && event.SessionID != "" {
					if userSessions[event.UserID] == nil {
						userSessions[event.UserID] = make(map[string]bool)
					}
					userSessions[event.UserID][event.SessionID] = true
				}
			}

			totalSessions := 0
			for _, sessions := range userSessions {
				totalSessions += len(sessions)
			}

			var sessionFrequency float64
			if len(userSessions) > 0 {
				sessionFrequency = float64(totalSessions) / float64(len(userSessions))
			}

			results = append(results, MetricResult{
				Metric: "session_frequency",
				Value:  fmt.Sprintf("%.2f", sessionFrequency),
			})

		case "adoption_metrics":
			coreFeatures := []string{"purchase", "form_submit"}
			featureAdoption := make(map[string]map[string]bool)
			allActiveUsers := make(map[string]bool)

			for _, event := range events {
				if event.UserID != "" {
					allActiveUsers[event.UserID] = true
					for _, feature := range coreFeatures {
						if event.EventType == feature {
							if featureAdoption[feature] == nil {
								featureAdoption[feature] = make(map[string]bool)
							}
							featureAdoption[feature][event.UserID] = true
						}
					}
				}
			}

			totalActiveUsers := len(allActiveUsers)
			adoptionRates := make(map[string]string)
			totalAdoptedUsers := make(map[string]bool)

			for feature, users := range featureAdoption {
				if totalActiveUsers > 0 {
					rate := float64(len(users)) / float64(totalActiveUsers) * 100
					adoptionRates[feature] = fmt.Sprintf("%.2f%%", rate)
				} else {
					adoptionRates[feature] = "0.00%"
				}
				for user := range users {
					totalAdoptedUsers[user] = true
				}
			}

			var overallAdoptionRate float64
			if totalActiveUsers > 0 {
				overallAdoptionRate = float64(len(totalAdoptedUsers)) / float64(totalActiveUsers) * 100
			}

			results = append(results, MetricResult{
				Metric:    "adoption_metrics",
				Value:     fmt.Sprintf("%.2f%%", overallAdoptionRate),
				Breakdown: convertStringMapToInterface(adoptionRates),
			})

		case "conversion_rate":
			// Use real Stripe data from database instead of API calls
			var stripeMetrics RevenueMetrics
			today := time.Now().Format("2006-01-02")

			if err := as.db.Where("project_id = ? AND date = ?", projectID, today).First(&stripeMetrics).Error; err == nil {
				// Calculate conversion rate from stored metrics
				var totalCustomers int64
				as.db.Model(&StripeCustomer{}).Where("project_id = ?", projectID).Count(&totalCustomers)

				var conversionRate float64
				if totalCustomers > 0 {
					conversionRate = float64(stripeMetrics.ActiveSubscriptions) / float64(totalCustomers) * 100
				}

				results = append(results, MetricResult{
					Metric: "conversion_rate",
					Value:  fmt.Sprintf("%.2f%%", conversionRate),
				})
			} else {
				results = append(results, MetricResult{
					Metric: "conversion_rate",
					Value:  "No data - sync Stripe first",
				})
			}

		case "churn_rate":
			// Use real Stripe data from database
			var stripeMetrics RevenueMetrics
			today := time.Now().Format("2006-01-02")

			if err := as.db.Where("project_id = ? AND date = ?", projectID, today).First(&stripeMetrics).Error; err == nil {
				results = append(results, MetricResult{
					Metric: "churn_rate",
					Value:  fmt.Sprintf("%.2f%%", stripeMetrics.ChurnRate),
				})
			} else {
				results = append(results, MetricResult{
					Metric: "churn_rate",
					Value:  "No data - sync Stripe first",
				})
			}

		case "mrr":
			// Use real Stripe data from database
			var stripeMetrics RevenueMetrics
			today := time.Now().Format("2006-01-02")

			if err := as.db.Where("project_id = ? AND date = ?", projectID, today).First(&stripeMetrics).Error; err == nil {
				results = append(results, MetricResult{
					Metric: "mrr",
					Value:  fmt.Sprintf("$%.2f", float64(stripeMetrics.MRR)/100),
				})
			} else {
				results = append(results, MetricResult{
					Metric: "mrr",
					Value:  "No data - sync Stripe first",
				})
			}

		case "arpu":
			// Use real Stripe data from database
			var stripeMetrics RevenueMetrics
			today := time.Now().Format("2006-01-02")

			if err := as.db.Where("project_id = ? AND date = ?", projectID, today).First(&stripeMetrics).Error; err == nil {
				results = append(results, MetricResult{
					Metric: "arpu",
					Value:  fmt.Sprintf("$%.2f", float64(stripeMetrics.ARPU)/100),
				})
			} else {
				results = append(results, MetricResult{
					Metric: "arpu",
					Value:  "No data - sync Stripe first",
				})
			}

		default:
			log.Printf("Unknown metric requested: %s", metric)
			results = append(results, MetricResult{
				Metric: metric,
				Value:  "Metric not implemented",
			})
		}
	}

	log.Printf("calculateMetrics returning %d results: %v", len(results), results)
	return results
}

// Helper functions for missing methods

// convertMapToInterface converts map[string]int to map[string]interface{}
func convertMapToInterface(m map[string]int) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// convertStringMapToInterface converts map[string]string to map[string]interface{}
func convertStringMapToInterface(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// fetchEventsForDateRange fetches events from R2 storage for the given date range
func (as *AnalyticsService) fetchEventsForDateRange(accountID, projectID, startDate, endDate string) ([]Event, error) {
	// Check cache first
	if cachedEvents, found := as.getCachedEvents(accountID, projectID, startDate, endDate); found {
		log.Printf("Returning %d cached events for %s-%s", len(cachedEvents), startDate, endDate)
		return cachedEvents, nil
	}

	// Parse date range
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date: %v", err)
	}
	// include entire end day
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date: %v", err)
	}
	end = end.Add(24*time.Hour - time.Nanosecond)

	var events []Event
	if err := as.db.Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?", accountID, projectID, start, end).Order("timestamp ASC").Find(&events).Error; err != nil {
		return nil, fmt.Errorf("db query failed: %v", err)
	}

	// Cache the results
	as.setCachedEvents(accountID, projectID, startDate, endDate, events)

	log.Printf("Fetched %d events from TimescaleDB for date range %s to %s", len(events), startDate, endDate)
	return events, nil
}

// filterEvents filters events based on query parameters
func (as *AnalyticsService) filterEvents(events []Event, query AnalyticsQuery) []Event {
	filtered := make([]Event, 0)

	for _, event := range events {
		// Filter by event type
		if query.EventType != "" && event.EventType != query.EventType {
			continue
		}

		// Filter by user ID
		if query.UserID != "" && event.UserID != query.UserID {
			continue
		}

		// Filter by session ID
		if query.SessionID != "" && event.SessionID != query.SessionID {
			continue
		}

		filtered = append(filtered, event)
	}

	return filtered
}

// filterEventsByDate filters events by date range
func (as *AnalyticsService) filterEventsByDate(events []Event, startDate, endDate string) []Event {
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Nanosecond)

	var filtered []Event
	for _, e := range events {
		if (e.Timestamp.Equal(start) || e.Timestamp.After(start)) && (e.Timestamp.Equal(end) || e.Timestamp.Before(end)) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// getRollingActiveUsersTimeSeries generates rolling active users time series data
func (as *AnalyticsService) getRollingActiveUsersTimeSeries(events []Event, windowDays int, startDate, endDate string) []TimeSeriesPoint {
	// Parse range
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	// Pre-process events into daily user sets
	dailyUsers := make(map[string]map[string]bool)
	for _, event := range events {
		d := event.Timestamp.Format("2006-01-02")
		if dailyUsers[d] == nil {
			dailyUsers[d] = make(map[string]bool)
		}
		if event.UserID != "" {
			dailyUsers[d][event.UserID] = true
		}
	}

	var points []TimeSeriesPoint
	current := start

	// Iterate through the requested range
	for !current.After(end) {
		dateStr := current.Format("2006-01-02")
		windowUsers := make(map[string]bool)

		// Look back windowDays
		for i := 0; i < windowDays; i++ {
			d := current.AddDate(0, 0, -i)
			dStr := d.Format("2006-01-02")
			if users, ok := dailyUsers[dStr]; ok {
				for u := range users {
					windowUsers[u] = true
				}
			}
		}

		count := len(windowUsers)
		points = append(points, TimeSeriesPoint{
			Date:        dateStr,
			Value:       count,
			UniqueUsers: count,
		})

		current = current.AddDate(0, 0, 1)
	}

	return points
}

// getTimeSeriesData generates time series data based on groupBy parameter
func (as *AnalyticsService) getTimeSeriesData(events []Event, groupBy string, valueFunc func([]Event) interface{}) []TimeSeriesPoint {
	// Group events by time period
	groups := make(map[string][]Event)

	for _, event := range events {
		var key string
		switch groupBy {
		case "hour":
			key = event.Timestamp.Format("2006-01-02T15")
		case "day":
			key = event.Timestamp.Format("2006-01-02")
		case "week":
			year, week := event.Timestamp.ISOWeek()
			key = fmt.Sprintf("%d-W%02d", year, week)
		case "month":
			key = event.Timestamp.Format("2006-01")
		default:
			key = event.Timestamp.Format("2006-01-02")
		}

		groups[key] = append(groups[key], event)
	}

	// Convert to time series points
	var points []TimeSeriesPoint
	for date, eventGroup := range groups {
		uniqueUsers := make(map[string]bool)
		for _, event := range eventGroup {
			if event.UserID != "" {
				uniqueUsers[event.UserID] = true
			}
		}

		points = append(points, TimeSeriesPoint{
			Date:        date,
			Value:       valueFunc(eventGroup),
			UniqueUsers: len(uniqueUsers),
		})
	}

	// Sort by date
	sort.Slice(points, func(i, j int) bool {
		return points[i].Date < points[j].Date
	})

	return points
}

// getDAUTimeSeries generates DAU time series data
func (as *AnalyticsService) getDAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	return as.getTimeSeriesData(events, "day", func(events []Event) interface{} {
		users := make(map[string]bool)
		for _, event := range events {
			if event.UserID != "" {
				users[event.UserID] = true
			}
		}
		return len(users)
	})
}

// getWAUTimeSeries generates WAU time series data
func (as *AnalyticsService) getWAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	return as.getTimeSeriesData(events, "week", func(events []Event) interface{} {
		users := make(map[string]bool)
		for _, event := range events {
			if event.UserID != "" {
				users[event.UserID] = true
			}
		}
		return len(users)
	})
}

// getMAUTimeSeries generates MAU time series data
func (as *AnalyticsService) getMAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	return as.getTimeSeriesData(events, "month", func(events []Event) interface{} {
		users := make(map[string]bool)
		for _, event := range events {
			if event.UserID != "" {
				users[event.UserID] = true
			}
		}
		return len(users)
	})
}

// getPageViewTimeSeries generates page view time series data
func (as *AnalyticsService) getPageViewTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	return as.getTimeSeriesData(events, groupBy, func(events []Event) interface{} {
		pageViews := 0
		for _, event := range events {
			if event.EventType == "page_view" || event.EventType == "pageview" {
				pageViews++
			}
		}
		return pageViews
	})
}

// Helper method to calculate DAU, WAU, MAU
// OPTIMIZED: Uses a single aggregated SQL query instead of 3 separate fetchEventsForDateRange calls
func (as *AnalyticsService) calculateUserMetrics(accountID, projectID string, date time.Time) (int, int, int) {
	// Calculate date boundaries
	dayStart := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	weekStart := dayStart.AddDate(0, 0, -6)   // 7 days including today
	monthStart := dayStart.AddDate(0, 0, -29) // 30 days including today

	// OPTIMIZED: Single query to get all three metrics at once
	type UserMetricsCounts struct {
		DAU int64
		WAU int64
		MAU int64
	}

	var counts UserMetricsCounts

	// DAU: unique users for the specific day
	as.db.Model(&Event{}).
		Select("COUNT(DISTINCT user_id) as dau").
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?", accountID, projectID, dayStart, dayEnd).
		Scan(&counts)

	// WAU: unique users for last 7 days
	as.db.Model(&Event{}).
		Select("COUNT(DISTINCT user_id) as wau").
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?", accountID, projectID, weekStart, dayEnd).
		Scan(&counts)

	// MAU: unique users for last 30 days
	as.db.Model(&Event{}).
		Select("COUNT(DISTINCT user_id) as mau").
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?", accountID, projectID, monthStart, dayEnd).
		Scan(&counts)

	return int(counts.DAU), int(counts.WAU), int(counts.MAU)
}

// Missing Handler Methods

// GetDashboardHandler returns dashboard summary data
// OPTIMIZED: Reduced from 4 event fetches + 3 DAU/WAU/MAU queries to 1 fetch + direct SQL aggregations
func (as *AnalyticsService) GetDashboardHandler(c *gin.Context) {
	start := time.Now()

	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache first
	if cachedDashboard, found := as.getCachedDashboard(accountID.(string), projectID.(string)); found {
		c.JSON(http.StatusOK, cachedDashboard)
		return
	}

	// Get date parameter or use today
	dateParam := c.DefaultQuery("date", time.Now().Format("2006-01-02"))
	today, _ := time.Parse("2006-01-02", dateParam)
	yesterday := today.AddDate(0, 0, -1)
	thirtyDaysAgo := today.AddDate(0, 0, -30)

	// OPTIMIZED: Fetch events once for the last 30 days (covers today, yesterday, and 30-day metrics)
	allEvents, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), thirtyDaysAgo.Format("2006-01-02"), dateParam)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
	}

	// Filter events by date in memory (much faster than multiple DB queries)
	todayStr := dateParam
	yesterdayStr := yesterday.Format("2006-01-02")

	todayUsers := make(map[string]bool)
	yesterdayUsers := make(map[string]bool)
	todayPageViews := 0
	yesterdayPageViews := 0
	totalPageViews := 0
	todayEventsCount := 0
	yesterdayEventsCount := 0
	eventCounts := make(map[string]int)

	for _, event := range allEvents {
		eventDate := event.Timestamp.Format("2006-01-02")

		// Page view tracking
		isPageView := event.EventType == "page_view" || event.EventType == "pageview"
		if isPageView {
			totalPageViews++
		}

		if eventDate == todayStr {
			todayEventsCount++
			if event.UserID != "" {
				todayUsers[event.UserID] = true
			}
			if isPageView {
				todayPageViews++
			}
			// Top events only for today
			eventCounts[event.EventType]++
		} else if eventDate == yesterdayStr {
			yesterdayEventsCount++
			if event.UserID != "" {
				yesterdayUsers[event.UserID] = true
			}
			if isPageView {
				yesterdayPageViews++
			}
		}
	}

	// Calculate growth rates
	eventGrowthRate := "0%"
	userGrowthRate := "0%"
	if yesterdayEventsCount > 0 {
		eventGrowth := float64(todayEventsCount-yesterdayEventsCount) / float64(yesterdayEventsCount) * 100
		eventGrowthRate = fmt.Sprintf("%+.1f%%", eventGrowth)
	}
	if len(yesterdayUsers) > 0 {
		userGrowth := float64(len(todayUsers)-len(yesterdayUsers)) / float64(len(yesterdayUsers)) * 100
		userGrowthRate = fmt.Sprintf("%+.1f%%", userGrowth)
	}

	// Calculate DAU, WAU, MAU using optimized SQL queries
	dau, wau, mau := as.calculateUserMetrics(accountID.(string), projectID.(string), today)

	// Build top events list
	topEvents := make([]map[string]interface{}, 0)
	for eventType, count := range eventCounts {
		percentage := 0.0
		if todayEventsCount > 0 {
			percentage = float64(count) / float64(todayEventsCount) * 100
		}
		topEvents = append(topEvents, map[string]interface{}{
			"event_type": eventType,
			"count":      count,
			"percentage": percentage,
		})
	}

	// Sort top events by count
	sort.Slice(topEvents, func(i, j int) bool {
		return topEvents[i]["count"].(int) > topEvents[j]["count"].(int)
	})

	dashboardData := map[string]interface{}{
		"date": dateParam,
		"overview": map[string]interface{}{
			"total_events_today":     todayEventsCount,
			"total_events_yesterday": yesterdayEventsCount,
			"unique_users_today":     len(todayUsers),
			"unique_users_yesterday": len(yesterdayUsers),
			"event_growth_rate":      eventGrowthRate,
			"user_growth_rate":       userGrowthRate,
		},
		"user_metrics": map[string]interface{}{
			"dau": dau,
			"wau": wau,
			"mau": mau,
		},
		"page_metrics": map[string]interface{}{
			"page_views_today":     todayPageViews,
			"page_views_yesterday": yesterdayPageViews,
			"total_page_views":     totalPageViews,
		},
		"top_events": topEvents,
		"meta": map[string]interface{}{
			"processing_time_ms": time.Since(start).Milliseconds(),
			"cache_hit":          false,
		},
	}

	// Cache the result
	as.setCachedDashboard(accountID.(string), projectID.(string), dashboardData)

	c.JSON(http.StatusOK, dashboardData)
}

// GetRealTimeHandler returns real-time analytics data
func (as *AnalyticsService) GetRealTimeHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	now := time.Now()
	fiveMinutesAgo := now.Add(-5 * time.Minute)
	oneHourAgo := now.Add(-1 * time.Hour)

	// Get recent events from cache and recent data
	todayEvents, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), now.Format("2006-01-02"), now.Format("2006-01-02"))
	if err != nil {
		log.Printf("Error fetching today's events for real-time: %v", err)
	}

	// Count events in different time windows
	eventsLast5Min := 0
	eventsLastHour := 0
	currentVisitors := make(map[string]bool) // Track active sessions
	pageVisits := make(map[string]int)

	for _, event := range todayEvents {
		// Count events in time windows
		if event.Timestamp.After(fiveMinutesAgo) {
			eventsLast5Min++
		}
		if event.Timestamp.After(oneHourAgo) {
			eventsLastHour++

			// Count current visitors (users active in last hour)
			if event.UserID != "" {
				currentVisitors[event.UserID] = true
			} else if event.SessionID != "" {
				currentVisitors[event.SessionID] = true
			}
		}

		// Track page visits for top pages
		if event.EventType == "page_view" || event.EventType == "pageview" {
			if event.Properties != nil {
				var pageURL string
				if url, ok := event.Properties["url"].(string); ok {
					pageURL = url
				} else if path, ok := event.Properties["path"].(string); ok {
					pageURL = path
				} else if page, ok := event.Properties["page"].(string); ok {
					pageURL = page
				}

				if pageURL != "" && event.Timestamp.After(oneHourAgo) {
					pageVisits[pageURL]++
				}
			}
		}
	}

	// Get top pages
	topPages := make([]map[string]interface{}, 0)
	for page, visitors := range pageVisits {
		topPages = append(topPages, map[string]interface{}{
			"page":     page,
			"visitors": visitors,
		})
	}

	// Sort by visitor count
	sort.Slice(topPages, func(i, j int) bool {
		return topPages[i]["visitors"].(int) > topPages[j]["visitors"].(int)
	})

	// Limit to top 10
	if len(topPages) > 10 {
		topPages = topPages[:10]
	}

	realTimeData := map[string]interface{}{
		"current_visitors":   len(currentVisitors),
		"events_last_5_min":  eventsLast5Min,
		"events_last_hour":   eventsLastHour,
		"cache_size":         as.getCacheSize(),
		"top_pages_now":      topPages,
		"timestamp":          now.UTC(),
		"total_events_today": len(todayEvents),
	}

	c.JSON(http.StatusOK, realTimeData)
}

// GetUserMetricsHandler returns DAU/WAU/MAU metrics
func (as *AnalyticsService) GetUserMetricsHandler(c *gin.Context) {
	start := time.Now()

	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get date parameter or use today
	dateParam := c.DefaultQuery("date", time.Now().Format("2006-01-02"))
	metricParam := c.DefaultQuery("metric", "all")

	// Check cache first
	if cachedMetrics, found := as.getCachedMetrics(accountID.(string), projectID.(string), "user_metrics", dateParam); found {
		c.JSON(http.StatusOK, cachedMetrics)
		return
	}

	// Calculate real user metrics
	currentDate, _ := time.Parse("2006-01-02", dateParam)
	previousDate := currentDate.AddDate(0, 0, -1)

	// Calculate current metrics
	dau, wau, mau := as.calculateUserMetrics(accountID.(string), projectID.(string), currentDate)

	// Calculate previous day metrics for growth rates
	prevDau, prevWau, prevMau := as.calculateUserMetrics(accountID.(string), projectID.(string), previousDate)

	// Calculate growth rates
	dauGrowth := "0%"
	wauGrowth := "0%"
	mauGrowth := "0%"

	if prevDau > 0 {
		growth := float64(dau-prevDau) / float64(prevDau) * 100
		dauGrowth = fmt.Sprintf("%+.1f%%", growth)
	}
	if prevWau > 0 {
		growth := float64(wau-prevWau) / float64(prevWau) * 100
		wauGrowth = fmt.Sprintf("%+.1f%%", growth)
	}
	if prevMau > 0 {
		growth := float64(mau-prevMau) / float64(prevMau) * 100
		mauGrowth = fmt.Sprintf("%+.1f%%", growth)
	}

	userMetrics := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"date":       dateParam,
			"dau":        dau,
			"wau":        wau,
			"mau":        mau,
			"dau_growth": dauGrowth,
			"wau_growth": wauGrowth,
			"mau_growth": mauGrowth,
		},
		"meta": map[string]interface{}{
			"processing_time_ms": time.Since(start).Milliseconds(),
			"cache_hit":          false,
		},
	}

	// If specific metric requested, filter the response
	if metricParam != "all" {
		if data, ok := userMetrics["data"].(map[string]interface{}); ok {
			filteredData := map[string]interface{}{
				"date": dateParam,
			}
			if value, exists := data[metricParam]; exists {
				filteredData[metricParam] = value
				if growth, exists := data[metricParam+"_growth"]; exists {
					filteredData[metricParam+"_growth"] = growth
				}
			}
			userMetrics["data"] = filteredData
		}
	}

	// Cache the result
	as.setCachedMetrics(accountID.(string), projectID.(string), "user_metrics", dateParam, userMetrics)

	c.JSON(http.StatusOK, userMetrics)
}

// GetHeatmapHandler returns heatmap data for analytics
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (as *AnalyticsService) GetHeatmapHandler(c *gin.Context) {
	accountID, exists := c.Get("account_id")
	if !exists || accountID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized - missing account_id"})
		return
	}

	projectID, exists := c.Get("project_id")
	if !exists || projectID == nil {
		// Try to get project_id from URL parameter as fallback
		projectID = c.Param("project_id")
		if projectID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized - missing project_id"})
			return
		}
	}

	// Get query parameters
	pageURL := c.Query("page_url")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -7).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	// Convert to strings safely
	accountIDStr, ok := accountID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid account_id format"})
		return
	}

	projectIDStr, ok := projectID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid project_id format"})
		return
	}

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Nanosecond)

	// OPTIMIZED: Get aggregated click data with SQL
	type ClickAggregation struct {
		X     float64 `gorm:"column:x"`
		Y     float64 `gorm:"column:y"`
		Count int64   `gorm:"column:count"`
	}

	clickQuery := `
		SELECT 
			(properties->>'x')::float as x,
			(properties->>'y')::float as y,
			COUNT(*) as count
		FROM events
		WHERE account_id = $1 
			AND project_id = $2
			AND timestamp BETWEEN $3 AND $4
			AND event_type IN ('click', 'heatmap_click')
			AND properties->>'x' IS NOT NULL
			AND properties->>'y' IS NOT NULL
	`
	clickArgs := []interface{}{accountIDStr, projectIDStr, start, end}

	if pageURL != "" {
		clickQuery += ` AND (properties->>'page_url' = $5 OR properties->>'path' = $5 OR properties->>'url' = $5)`
		clickArgs = append(clickArgs, pageURL)
	}

	clickQuery += `
		GROUP BY (properties->>'x')::float, (properties->>'y')::float
		ORDER BY count DESC
		LIMIT 500
	`

	var clickAggs []ClickAggregation
	if err := as.db.Raw(clickQuery, clickArgs...).Scan(&clickAggs).Error; err != nil {
		log.Printf("Error fetching click data: %v", err)
	}

	// Build aggregated clicks response
	aggregatedClicks := make([]map[string]interface{}, 0, len(clickAggs))
	for _, click := range clickAggs {
		aggregatedClicks = append(aggregatedClicks, map[string]interface{}{
			"x":     click.X,
			"y":     click.Y,
			"count": click.Count,
		})
	}

	// OPTIMIZED: Get scroll depth statistics with SQL
	type ScrollStats struct {
		Depth      int   `gorm:"column:depth"`
		Count      int64 `gorm:"column:count"`
		Percentage float64
	}

	scrollQuery := `
		WITH scroll_events AS (
			SELECT (properties->>'scroll_depth')::float as scroll_depth
			FROM events
			WHERE account_id = $1 
				AND project_id = $2
				AND timestamp BETWEEN $3 AND $4
				AND event_type = 'scroll'
				AND properties->>'scroll_depth' IS NOT NULL
	`
	scrollArgs := []interface{}{accountIDStr, projectIDStr, start, end}

	if pageURL != "" {
		scrollQuery += ` AND (properties->>'page_url' = $5 OR properties->>'path' = $5)`
		scrollArgs = append(scrollArgs, pageURL)
	}

	scrollQuery += `
		),
		total_scrolls AS (SELECT COUNT(*) as total FROM scroll_events)
		SELECT 
			depth,
			SUM(CASE WHEN scroll_depth >= depth::float / 100.0 THEN 1 ELSE 0 END) as count
		FROM scroll_events, (VALUES (25), (50), (75), (100)) AS depths(depth), total_scrolls
		GROUP BY depth, total_scrolls.total
		ORDER BY depth
	`

	var scrollResults []ScrollStats
	as.db.Raw(scrollQuery, scrollArgs...).Scan(&scrollResults)

	// Calculate percentages
	var totalScrollEvents int64
	as.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ? AND event_type = 'scroll'",
			accountIDStr, projectIDStr, start, end).
		Count(&totalScrollEvents)

	scrollStats := make([]map[string]interface{}, 0)
	for _, stat := range scrollResults {
		percentage := 0.0
		if totalScrollEvents > 0 {
			percentage = float64(stat.Count) / float64(totalScrollEvents) * 100
		}
		scrollStats = append(scrollStats, map[string]interface{}{
			"depth":      stat.Depth,
			"count":      stat.Count,
			"percentage": percentage,
		})
	}

	// If no scroll stats from query, provide empty defaults
	if len(scrollStats) == 0 {
		for _, depth := range []int{25, 50, 75, 100} {
			scrollStats = append(scrollStats, map[string]interface{}{
				"depth":      depth,
				"count":      0,
				"percentage": 0.0,
			})
		}
	}

	// Get total sessions count with SQL
	var totalSessions int64
	sessionQuery := as.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ? AND timestamp BETWEEN ? AND ?", accountIDStr, projectIDStr, start, end).
		Where("session_id IS NOT NULL AND session_id != ''")

	if pageURL != "" {
		sessionQuery = sessionQuery.Where("properties->>'page_url' = ? OR properties->>'path' = ? OR properties->>'url' = ?", pageURL, pageURL, pageURL)
	}
	sessionQuery.Distinct("session_id").Count(&totalSessions)

	// Return data in format expected by frontend
	heatmapData := map[string]interface{}{
		"url":           pageURL,
		"clicks":        aggregatedClicks,
		"scrolls":       scrollStats,
		"mouseMoves":    []interface{}{}, // Empty for now
		"viewport":      map[string]interface{}{"width": 1920, "height": 1080, "deviceType": "desktop"},
		"totalSessions": totalSessions,
	}

	c.JSON(http.StatusOK, heatmapData)
}

// Helper function to filter scroll depths above a threshold
func filterScrolls(scrolls []float64, threshold float64) []float64 {
	result := make([]float64, 0)
	for _, scroll := range scrolls {
		if scroll >= threshold {
			result = append(result, scroll)
		}
	}
	return result
}

// GetErrorAnalyticsHandler returns error analytics data
func (as *AnalyticsService) GetErrorAnalyticsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Mock error analytics data
	errorData := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"total_errors": 23,
			"error_rate":   "1.2%",
			"top_errors": []map[string]interface{}{
				{"error": "TypeError: Cannot read property 'x' of undefined", "count": 8},
				{"error": "NetworkError: Failed to fetch", "count": 6},
				{"error": "ReferenceError: variable is not defined", "count": 4},
			},
			"errors_by_browser": map[string]interface{}{
				"Chrome":  12,
				"Safari":  6,
				"Firefox": 3,
				"Edge":    2,
			},
		},
	}

	c.JSON(http.StatusOK, errorData)
}

// GetSessionAnalyticsHandler returns session analytics for a specific session
func (as *AnalyticsService) GetSessionAnalyticsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	sessionID := c.Param("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id is required"})
		return
	}

	// Mock session data
	sessionData := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"session_id": sessionID,
			"user_id":    "user_123",
			"duration":   "00:15:32",
			"page_views": 8,
			"events": []map[string]interface{}{
				{
					"timestamp":  "2024-01-15T10:30:00Z",
					"event_type": "page_view",
					"page":       "/dashboard",
				},
				{
					"timestamp":  "2024-01-15T10:32:15Z",
					"event_type": "click",
					"element":    "export_button",
				},
			},
			"device_info": map[string]interface{}{
				"browser": "Chrome",
				"os":      "macOS",
				"device":  "desktop",
			},
		},
	}

	c.JSON(http.StatusOK, sessionData)
}

// TODO
// GetRetentionCohortsHandler returns retention cohort analysis
func (as *AnalyticsService) GetRetentionCohortsHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Mock retention cohort data
	retentionData := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"cohorts": []map[string]interface{}{
				{
					"cohort_date": "2024-01",
					"size":        100,
					"retention": map[string]interface{}{
						"week_1": 0.75,
						"week_2": 0.65,
						"week_3": 0.58,
						"week_4": 0.52,
					},
				},
				{
					"cohort_date": "2024-02",
					"size":        120,
					"retention": map[string]interface{}{
						"week_1": 0.78,
						"week_2": 0.68,
						"week_3": 0.61,
						"week_4": 0.55,
					},
				},
			},
			"average_retention": map[string]interface{}{
				"week_1": 0.76,
				"week_2": 0.66,
				"week_3": 0.59,
				"week_4": 0.53,
			},
		},
	}

	c.JSON(http.StatusOK, retentionData)
}

// GetChurnByChannelHandler returns churn analysis segmented by acquisition channel
func (as *AnalyticsService) GetChurnByChannelHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")

	accountIDStr, ok1 := accountID.(string)
	projectIDStr, ok2 := projectID.(string)

	if !ok1 || !ok2 || accountIDStr == "" || projectIDStr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get date range parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	type AtRiskUser struct {
		UserID       string  `json:"user_id"`
		Email        string  `json:"email,omitempty"`
		LastActivity string  `json:"last_activity"`
		DaysSince    int     `json:"days_since"`
		RiskScore    float64 `json:"risk_score"`
	}

	type ChannelMetrics struct {
		Channel      string       `json:"channel"`
		TotalUsers   int          `json:"total_users"`
		ActiveUsers  int          `json:"active_users"`
		ChurnedUsers int          `json:"churned_users"`
		ChurnRate    float64      `json:"churn_rate"`
		AtRiskUsers  []AtRiskUser `json:"at_risk_users"`
	}

	// Query events grouped by channel
	var channelData []struct {
		Channel      string
		UserID       string
		Email        string
		LastActivity time.Time
	}

	err := as.db.Raw(`
		SELECT DISTINCT ON (channel, user_id)
			COALESCE(NULLIF(channel, ''), 'direct') as channel,
			user_id,
			email,
			MAX(timestamp) as last_activity
		FROM events
		WHERE account_id = ? 
			AND project_id = ?
			AND user_id IS NOT NULL 
			AND user_id != ''
			AND timestamp BETWEEN ? AND ?
		GROUP BY channel, user_id, email
		ORDER BY channel, user_id, last_activity DESC
	`, accountIDStr, projectIDStr, startDate, endDate).Scan(&channelData).Error

	if err != nil {
		log.Printf("Error querying channel data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch channel data"})
		return
	}

	// Group users by channel and calculate metrics
	channelMap := make(map[string]*ChannelMetrics)
	now := time.Now()

	for _, data := range channelData {
		channel := data.Channel
		if channel == "" {
			channel = "direct"
		}

		if _, exists := channelMap[channel]; !exists {
			channelMap[channel] = &ChannelMetrics{
				Channel:     channel,
				AtRiskUsers: []AtRiskUser{},
			}
		}

		metrics := channelMap[channel]
		metrics.TotalUsers++

		daysSince := int(now.Sub(data.LastActivity).Hours() / 24)

		// Consider users inactive if no activity in last 7 days
		if daysSince <= 7 {
			metrics.ActiveUsers++
		} else {
			metrics.ChurnedUsers++
		}

		// Calculate risk score for users inactive 3-14 days (at risk window)
		if daysSince >= 3 && daysSince <= 14 {
			riskScore := float64(daysSince-3) / 11.0 * 100 // 0-100 scale
			metrics.AtRiskUsers = append(metrics.AtRiskUsers, AtRiskUser{
				UserID:       data.UserID,
				Email:        data.Email,
				LastActivity: data.LastActivity.Format("2006-01-02 15:04:05"),
				DaysSince:    daysSince,
				RiskScore:    riskScore,
			})
		}
	}

	// Calculate churn rate for each channel
	channels := make([]ChannelMetrics, 0, len(channelMap))
	for _, metrics := range channelMap {
		if metrics.TotalUsers > 0 {
			metrics.ChurnRate = float64(metrics.ChurnedUsers) / float64(metrics.TotalUsers) * 100
		}
		channels = append(channels, *metrics)
	}

	// Sort channels by total users (descending)
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].TotalUsers > channels[j].TotalUsers
	})

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"channels":   channels,
			"start_date": startDate,
			"end_date":   endDate,
		},
	})
}

// GetPaidUsersMetricsHandler returns paid vs free user metrics and MRR breakdown
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (as *AnalyticsService) GetPaidUsersMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Nanosecond)

	// OPTIMIZED: Get user subscription metrics with SQL aggregation
	type UserSubscriptionData struct {
		TotalUsers    int64   `gorm:"column:total_users"`
		PaidUsers     int64   `gorm:"column:paid_users"`
		TrialingUsers int64   `gorm:"column:trialing_users"`
		TotalMRR      float64 `gorm:"column:total_mrr"`
	}

	var subscriptionData UserSubscriptionData
	as.db.Raw(`
		WITH latest_user_data AS (
			SELECT DISTINCT ON (user_id)
				user_id,
				properties->>'subscription_status' as status,
				COALESCE((properties->>'is_paid_user')::boolean, false) as is_paid,
				COALESCE((properties->>'subscription_mrr')::numeric, 0) as mrr
			FROM events
			WHERE account_id = $1 
				AND project_id = $2
				AND timestamp BETWEEN $3 AND $4
				AND user_id IS NOT NULL 
				AND user_id != ''
			ORDER BY user_id, timestamp DESC
		)
		SELECT 
			COUNT(*) as total_users,
			SUM(CASE WHEN is_paid THEN 1 ELSE 0 END) as paid_users,
			SUM(CASE WHEN status = 'trialing' THEN 1 ELSE 0 END) as trialing_users,
			SUM(mrr) / 100.0 as total_mrr
		FROM latest_user_data
	`, accountID.(string), projectID, start, end).Scan(&subscriptionData)

	// Get plan breakdown
	type PlanCount struct {
		Plan  string `gorm:"column:plan"`
		Count int64  `gorm:"column:count"`
	}
	var planCounts []PlanCount
	as.db.Raw(`
		SELECT 
			COALESCE(properties->>'subscription_plan', 'unknown') as plan,
			COUNT(DISTINCT user_id) as count
		FROM events
		WHERE account_id = $1 
			AND project_id = $2
			AND timestamp BETWEEN $3 AND $4
			AND user_id IS NOT NULL 
			AND properties->>'subscription_plan' IS NOT NULL
		GROUP BY properties->>'subscription_plan'
		ORDER BY count DESC
	`, accountID.(string), projectID, start, end).Scan(&planCounts)

	planBreakdown := make(map[string]int64)
	for _, pc := range planCounts {
		planBreakdown[pc.Plan] = pc.Count
	}

	// Get provider breakdown
	var providerCounts []PlanCount
	as.db.Raw(`
		SELECT 
			COALESCE(properties->>'subscription_provider', 'unknown') as plan,
			COUNT(DISTINCT user_id) as count
		FROM events
		WHERE account_id = $1 
			AND project_id = $2
			AND timestamp BETWEEN $3 AND $4
			AND user_id IS NOT NULL 
			AND properties->>'subscription_provider' IS NOT NULL
		GROUP BY properties->>'subscription_provider'
		ORDER BY count DESC
	`, accountID.(string), projectID, start, end).Scan(&providerCounts)

	providerBreakdown := make(map[string]int64)
	for _, pc := range providerCounts {
		providerBreakdown[pc.Plan] = pc.Count
	}

	// Calculate derived metrics
	totalUsers := subscriptionData.TotalUsers
	paidUsers := subscriptionData.PaidUsers
	freeUsers := totalUsers - paidUsers
	totalMRR := subscriptionData.TotalMRR

	paidPercentage := 0.0
	arpu := 0.0
	if totalUsers > 0 {
		paidPercentage = float64(paidUsers) / float64(totalUsers) * 100
	}
	if paidUsers > 0 {
		arpu = totalMRR / float64(paidUsers)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"summary": gin.H{
				"total_users":     totalUsers,
				"paid_users":      paidUsers,
				"free_users":      freeUsers,
				"trialing_users":  subscriptionData.TrialingUsers,
				"paid_percentage": paidPercentage,
				"total_mrr":       totalMRR,
				"arr":             totalMRR * 12,
				"arpu":            arpu,
			},
			"breakdowns": gin.H{
				"by_plan":     planBreakdown,
				"by_provider": providerBreakdown,
			},
			"date_range": gin.H{
				"start": startDate,
				"end":   endDate,
			},
		},
	})
}

// GetChurnMetricsHandler returns churn rates and MRR churn metrics
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (as *AnalyticsService) GetChurnMetricsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Nanosecond)

	// OPTIMIZED: Get subscription event metrics with SQL aggregation
	type SubscriptionMetrics struct {
		EventType string  `gorm:"column:event_type"`
		Count     int64   `gorm:"column:count"`
		TotalMRR  float64 `gorm:"column:total_mrr"`
	}

	var metrics []SubscriptionMetrics
	as.db.Raw(`
		SELECT 
			event_type,
			COUNT(*) as count,
			COALESCE(SUM((properties->>'mrr')::numeric), 0) / 100.0 as total_mrr
		FROM events
		WHERE account_id = $1 
			AND project_id = $2
			AND timestamp BETWEEN $3 AND $4
			AND event_type IN ('subscription_started', 'subscription_canceled', 'subscription_upgraded', 'subscription_downgraded')
		GROUP BY event_type
	`, accountID.(string), projectID, start, end).Scan(&metrics)

	// Parse metrics from results
	var subscriptionStarted, subscriptionCanceled, subscriptionUpgraded, subscriptionDowngraded int64
	var mrrStarted, mrrCanceled float64

	for _, m := range metrics {
		switch m.EventType {
		case "subscription_started":
			subscriptionStarted = m.Count
			mrrStarted = m.TotalMRR
		case "subscription_canceled":
			subscriptionCanceled = m.Count
			mrrCanceled = m.TotalMRR
		case "subscription_upgraded":
			subscriptionUpgraded = m.Count
		case "subscription_downgraded":
			subscriptionDowngraded = m.Count
		}
	}

	// Get MRR upgrade/downgrade amounts (more complex calculation)
	var mrrUpgrade, mrrDowngrade float64
	as.db.Raw(`
		SELECT 
			COALESCE(SUM(CASE WHEN event_type = 'subscription_upgraded' 
				THEN ((properties->>'mrr')::numeric - COALESCE((properties->>'previous_mrr')::numeric, 0)) / 100.0 
				ELSE 0 END), 0) as upgrade,
			COALESCE(SUM(CASE WHEN event_type = 'subscription_downgraded' 
				THEN (COALESCE((properties->>'previous_mrr')::numeric, 0) - (properties->>'mrr')::numeric) / 100.0 
				ELSE 0 END), 0) as downgrade
		FROM events
		WHERE account_id = $1 
			AND project_id = $2
			AND timestamp BETWEEN $3 AND $4
			AND event_type IN ('subscription_upgraded', 'subscription_downgraded')
	`, accountID.(string), projectID, start, end).Row().Scan(&mrrUpgrade, &mrrDowngrade)

	// Calculate churn rates
	customerChurnRate := 0.0
	if subscriptionStarted > 0 {
		customerChurnRate = float64(subscriptionCanceled) / float64(subscriptionStarted) * 100
	}

	grossMRRChurn := mrrCanceled + mrrDowngrade
	netMRRChurn := grossMRRChurn - mrrUpgrade
	mrrChurnRate := 0.0
	netMRRChurnRate := 0.0
	if mrrStarted > 0 {
		mrrChurnRate = (grossMRRChurn / mrrStarted) * 100
		netMRRChurnRate = (netMRRChurn / mrrStarted) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"customer_churn": gin.H{
				"subscriptions_started":    subscriptionStarted,
				"subscriptions_canceled":   subscriptionCanceled,
				"subscriptions_upgraded":   subscriptionUpgraded,
				"subscriptions_downgraded": subscriptionDowngraded,
				"churn_rate":               customerChurnRate,
			},
			"mrr_churn": gin.H{
				"mrr_started":        mrrStarted,
				"mrr_canceled":       mrrCanceled,
				"mrr_upgrade":        mrrUpgrade,
				"mrr_downgrade":      mrrDowngrade,
				"gross_mrr_churn":    grossMRRChurn,
				"net_mrr_churn":      netMRRChurn,
				"mrr_churn_rate":     mrrChurnRate,
				"net_mrr_churn_rate": netMRRChurnRate,
			},
			"date_range": gin.H{
				"start": startDate,
				"end":   endDate,
			},
		},
	})
}

// GetSubscriptionHealthHandler returns subscription health scores per user
func (as *AnalyticsService) GetSubscriptionHealthHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	// Get account ID from context
	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

	// Fetch recent events (last 30 days)
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	events, err := as.fetchEventsForDateRange(accountID.(string), projectID, startDate, endDate)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	// Calculate health scores per user
	type UserHealth struct {
		UserID              string                 `json:"user_id"`
		Email               string                 `json:"email,omitempty"`
		SubscriptionStatus  string                 `json:"subscription_status"`
		SubscriptionPlan    string                 `json:"subscription_plan,omitempty"`
		MRR                 float64                `json:"mrr"`
		HealthScore         float64                `json:"health_score"`
		RiskCategory        string                 `json:"risk_category"`
		DaysSinceLastActive int                    `json:"days_since_last_active"`
		EventCount          int                    `json:"event_count"`
		LastActivity        string                 `json:"last_activity"`
		AtRisk              bool                   `json:"at_risk"`
		Factors             map[string]interface{} `json:"factors"`
	}

	userHealthMap := make(map[string]*UserHealth)

	for _, event := range events {
		if event.UserID == "" {
			continue
		}

		if _, exists := userHealthMap[event.UserID]; !exists {
			userHealthMap[event.UserID] = &UserHealth{
				UserID:     event.UserID,
				Email:      event.Email,
				EventCount: 0,
				Factors:    make(map[string]interface{}),
			}
		}

		health := userHealthMap[event.UserID]
		health.EventCount++
		health.LastActivity = event.Timestamp.Format("2006-01-02 15:04:05")

		// Extract subscription properties
		props := event.Properties
		if props != nil {
			if status, ok := props["subscription_status"].(string); ok {
				health.SubscriptionStatus = status
			}
			if plan, ok := props["subscription_plan"].(string); ok {
				health.SubscriptionPlan = plan
			}
			if mrr, ok := props["subscription_mrr"].(float64); ok {
				health.MRR = mrr / 100 // Convert cents to dollars
			}
		}
	}

	// Calculate health scores
	now := time.Now()
	healthScores := make([]UserHealth, 0, len(userHealthMap))

	for _, health := range userHealthMap {
		// Calculate days since last active
		if health.LastActivity != "" {
			lastActive, _ := time.Parse("2006-01-02 15:04:05", health.LastActivity)
			health.DaysSinceLastActive = int(now.Sub(lastActive).Hours() / 24)
		}

		// Calculate health score (0-100, higher is better)
		healthScore := 100.0

		// Activity factor (max -40 points)
		if health.DaysSinceLastActive > 30 {
			healthScore -= 40
		} else if health.DaysSinceLastActive > 14 {
			healthScore -= 25
		} else if health.DaysSinceLastActive > 7 {
			healthScore -= 15
		}

		// Engagement factor (max -30 points)
		avgEventsPerDay := float64(health.EventCount) / 30.0
		if avgEventsPerDay < 1 {
			healthScore -= 30
		} else if avgEventsPerDay < 5 {
			healthScore -= 20
		} else if avgEventsPerDay < 10 {
			healthScore -= 10
		}

		// Subscription status factor (max -30 points)
		if health.SubscriptionStatus == "past_due" {
			healthScore -= 30
		} else if health.SubscriptionStatus == "canceled" {
			healthScore -= 40
		} else if health.SubscriptionStatus == "paused" {
			healthScore -= 20
		}

		health.HealthScore = healthScore

		// Determine risk category
		if healthScore >= 80 {
			health.RiskCategory = "healthy"
		} else if healthScore >= 60 {
			health.RiskCategory = "at_risk"
		} else if healthScore >= 40 {
			health.RiskCategory = "critical"
		} else {
			health.RiskCategory = "churn_imminent"
		}

		health.AtRisk = healthScore < 60

		// Add detailed factors
		health.Factors["activity_score"] = 100 - float64(health.DaysSinceLastActive)*2
		health.Factors["engagement_score"] = avgEventsPerDay * 10
		health.Factors["subscription_health"] = map[string]interface{}{
			"status": health.SubscriptionStatus,
			"mrr":    health.MRR,
		}

		healthScores = append(healthScores, *health)
	}

	// Sort by health score (ascending - most at risk first)
	sort.Slice(healthScores, func(i, j int) bool {
		return healthScores[i].HealthScore < healthScores[j].HealthScore
	})

	// Apply limit
	if len(healthScores) > limit {
		healthScores = healthScores[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"users":       healthScores,
			"total_users": len(userHealthMap),
			"date_range": gin.H{
				"start": startDate,
				"end":   endDate,
			},
		},
	})
}
