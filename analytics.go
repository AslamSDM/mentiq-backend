package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
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

func (as *AnalyticsService) GetAnalyticsHandler(c *gin.Context) {
	start := time.Now()

	var query AnalyticsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query parameters"})
		return
	}

	// Robustly handle metrics parameter
	// 1. Start with what Gin bound (might be []string{"a,b,c"} or []string{"a", "b"})
	rawMetrics := query.Metrics
	query.Metrics = make([]string, 0)

	// 2. Also check the raw query param in case Gin missed it or bound it weirdly
	if val := c.Query("metrics"); val != "" {
		// If we have a raw string, split it and use it if it looks richer than what Gin gave
		parts := strings.Split(val, ",")
		if len(parts) > len(rawMetrics) {
			rawMetrics = parts
		}
	}

	// 3. Flatten and clean
	for _, m := range rawMetrics {
		// Split by comma just in case we have "a,b" as a single element
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

	// Get Stripe API key from project (optional - currently not stored in Project model)
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

	// Determine fetch range (extended for rolling windows)
	fetchStartDate := query.StartDate
	needsRolling := false
	for _, m := range query.Metrics {
		if m == "wau" || m == "mau" {
			needsRolling = true
		}
	}
	if needsRolling {
		t, err := time.Parse("2006-01-02", query.StartDate)
		if err == nil {
			fetchStartDate = t.AddDate(0, 0, -30).Format("2006-01-02")
		}
	}

	events, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), fetchStartDate, query.EndDate)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	filteredEvents := as.filterEvents(events, query)
	standardEvents := as.filterEventsByDate(filteredEvents, query.StartDate, query.EndDate)

	results := as.calculateMetrics(standardEvents, filteredEvents, query, sc, projectID.(string))
	fmt.Printf("%+v\n", results)
	response := AnalyticsResponse{
		Query:   query,
		Results: results,
	}
	response.Meta.TotalEvents = len(standardEvents)
	response.Meta.ProcessingTime = time.Since(start)
	response.Meta.DateRange = fmt.Sprintf("%s to %s", query.StartDate, query.EndDate)

	c.JSON(http.StatusOK, response)
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
			today := time.Now().UTC().Format("2006-01-02")
			todayUsers := make(map[string]bool)

			for _, event := range events {
				eventDate := event.Timestamp.Format("2006-01-02")
				if eventDate == today && event.UserID != "" {
					todayUsers[event.UserID] = true
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
				Value:      len(todayUsers),
				TimeSeries: timeSeries,
			})

		case "wau":
			sevenDaysAgo := time.Now().UTC().AddDate(0, 0, -7)
			weeklyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(sevenDaysAgo) && event.UserID != "" {
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
			thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)
			monthlyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(thirtyDaysAgo) && event.UserID != "" {
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
func (as *AnalyticsService) calculateUserMetrics(accountID, projectID string, date time.Time) (int, int, int) {
	today := date.Format("2006-01-02")
	sevenDaysAgo := date.AddDate(0, 0, -7).Format("2006-01-02")
	thirtyDaysAgo := date.AddDate(0, 0, -30).Format("2006-01-02")

	// Get events for different periods
	dauEvents, _ := as.fetchEventsForDateRange(accountID, projectID, today, today)
	wauEvents, _ := as.fetchEventsForDateRange(accountID, projectID, sevenDaysAgo, today)
	mauEvents, _ := as.fetchEventsForDateRange(accountID, projectID, thirtyDaysAgo, today)

	// Count unique users for each period
	dauUsers := make(map[string]bool)
	wauUsers := make(map[string]bool)
	mauUsers := make(map[string]bool)

	for _, event := range dauEvents {
		if event.UserID != "" {
			dauUsers[event.UserID] = true
		}
	}

	for _, event := range wauEvents {
		if event.UserID != "" {
			wauUsers[event.UserID] = true
		}
	}

	for _, event := range mauEvents {
		if event.UserID != "" {
			mauUsers[event.UserID] = true
		}
	}

	return len(dauUsers), len(wauUsers), len(mauUsers)
}

// Missing Handler Methods

// GetDashboardHandler returns dashboard summary data
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

	// Calculate real metrics from events
	todayEvents, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), dateParam, dateParam)
	if err != nil {
		log.Printf("Error fetching today's events: %v", err)
	}

	yesterdayEvents, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), yesterday.Format("2006-01-02"), yesterday.Format("2006-01-02"))
	if err != nil {
		log.Printf("Error fetching yesterday's events: %v", err)
	}

	// Calculate unique users
	todayUsers := make(map[string]bool)
	yesterdayUsers := make(map[string]bool)
	todayPageViews := 0
	yesterdayPageViews := 0

	for _, event := range todayEvents {
		if event.UserID != "" {
			todayUsers[event.UserID] = true
		}
		if event.EventType == "page_view" || event.EventType == "pageview" {
			todayPageViews++
		}
	}

	for _, event := range yesterdayEvents {
		if event.UserID != "" {
			yesterdayUsers[event.UserID] = true
		}
		if event.EventType == "page_view" || event.EventType == "pageview" {
			yesterdayPageViews++
		}
	}

	// Calculate growth rates
	eventGrowthRate := "0%"
	userGrowthRate := "0%"
	if len(yesterdayEvents) > 0 {
		eventGrowth := float64(len(todayEvents)-len(yesterdayEvents)) / float64(len(yesterdayEvents)) * 100
		eventGrowthRate = fmt.Sprintf("%+.1f%%", eventGrowth)
	}
	if len(yesterdayUsers) > 0 {
		userGrowth := float64(len(todayUsers)-len(yesterdayUsers)) / float64(len(yesterdayUsers)) * 100
		userGrowthRate = fmt.Sprintf("%+.1f%%", userGrowth)
	}

	// Calculate DAU, WAU, MAU
	dau, wau, mau := as.calculateUserMetrics(accountID.(string), projectID.(string), today)

	// Calculate top events
	eventCounts := make(map[string]int)
	totalEvents := 0
	for _, event := range todayEvents {
		eventCounts[event.EventType]++
		totalEvents++
	}

	topEvents := make([]map[string]interface{}, 0)
	for eventType, count := range eventCounts {
		percentage := 0.0
		if totalEvents > 0 {
			percentage = float64(count) / float64(totalEvents) * 100
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

	// Get total page views from last 30 days
	thirtyDaysAgo := today.AddDate(0, 0, -30)
	thirtyDayEvents, _ := as.fetchEventsForDateRange(accountID.(string), projectID.(string), thirtyDaysAgo.Format("2006-01-02"), dateParam)
	totalPageViews := 0
	for _, event := range thirtyDayEvents {
		if event.EventType == "page_view" || event.EventType == "pageview" {
			totalPageViews++
		}
	}

	dashboardData := map[string]interface{}{
		"date": dateParam,
		"overview": map[string]interface{}{
			"total_events_today":     len(todayEvents),
			"total_events_yesterday": len(yesterdayEvents),
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

	// Fetch real heatmap events from date range
	events, err := as.fetchEventsForDateRange(accountIDStr, projectIDStr, startDate, endDate)
	if err != nil {
		log.Printf("Error fetching heatmap events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch heatmap data"})
		return
	}

	// Process heatmap data from events
	pageHeatmaps := make(map[string][]map[string]interface{})
	scrollData := make(map[string][]float64) // page -> scroll depths

	for _, event := range events {
		if event.EventType == "click" || event.EventType == "heatmap_click" {
			if event.Properties != nil {
				// Get page URL from event properties
				eventPageURL := ""
				if pageURLProp, ok := event.Properties["page_url"].(string); ok {
					eventPageURL = pageURLProp
				} else if pathProp, ok := event.Properties["path"].(string); ok {
					eventPageURL = pathProp
				} else if urlProp, ok := event.Properties["url"].(string); ok {
					eventPageURL = urlProp
				}

				// Filter by page URL if specified
				if pageURL != "" && eventPageURL != pageURL {
					continue
				}

				// Extract coordinates
				if x, okX := event.Properties["x"]; okX {
					if y, okY := event.Properties["y"]; okY {
						clickData := map[string]interface{}{
							"x":     x,
							"y":     y,
							"count": 1, // We'll aggregate these later
						}

						if eventPageURL == "" {
							eventPageURL = "unknown"
						}

						pageHeatmaps[eventPageURL] = append(pageHeatmaps[eventPageURL], clickData)
					}
				}
			}
		} else if event.EventType == "scroll" {
			if event.Properties != nil {
				// Get page URL and scroll depth
				eventPageURL := ""
				if pageURLProp, ok := event.Properties["page_url"].(string); ok {
					eventPageURL = pageURLProp
				} else if pathProp, ok := event.Properties["path"].(string); ok {
					eventPageURL = pathProp
				}

				if scrollDepth, ok := event.Properties["scroll_depth"]; ok {
					if depth, ok := scrollDepth.(float64); ok {
						if eventPageURL == "" {
							eventPageURL = "unknown"
						}
						scrollData[eventPageURL] = append(scrollData[eventPageURL], depth)
					}
				}
			}
		}
	}

	// Aggregate click data (merge clicks at same coordinates)
	aggregatedHeatmaps := make([]map[string]interface{}, 0)
	for page, clicks := range pageHeatmaps {
		clickCounts := make(map[string]map[string]interface{})

		for _, click := range clicks {
			key := fmt.Sprintf("%v,%v", click["x"], click["y"])
			if existing, exists := clickCounts[key]; exists {
				existing["count"] = existing["count"].(int) + 1
			} else {
				clickCounts[key] = map[string]interface{}{
					"x":     click["x"],
					"y":     click["y"],
					"count": 1,
				}
			}
		}

		// Convert to slice
		aggregatedClicks := make([]map[string]interface{}, 0)
		for _, clickData := range clickCounts {
			aggregatedClicks = append(aggregatedClicks, clickData)
		}

		// Sort by count (highest first)
		sort.Slice(aggregatedClicks, func(i, j int) bool {
			return aggregatedClicks[i]["count"].(int) > aggregatedClicks[j]["count"].(int)
		})

		aggregatedHeatmaps = append(aggregatedHeatmaps, map[string]interface{}{
			"page":   page,
			"clicks": aggregatedClicks,
		})
	}

	// Calculate scroll depth statistics
	scrollStats := make(map[string]float64)
	if len(scrollData) > 0 {
		// Combine all scroll data
		allScrolls := make([]float64, 0)
		for _, scrolls := range scrollData {
			allScrolls = append(allScrolls, scrolls...)
		}

		if len(allScrolls) > 0 {
			sort.Float64s(allScrolls)

			// Calculate percentages of users who reached certain depths
			total := float64(len(allScrolls))
			scrollStats["25%"] = float64(len(filterScrolls(allScrolls, 0.25))) / total
			scrollStats["50%"] = float64(len(filterScrolls(allScrolls, 0.50))) / total
			scrollStats["75%"] = float64(len(filterScrolls(allScrolls, 0.75))) / total
			scrollStats["100%"] = float64(len(filterScrolls(allScrolls, 1.0))) / total
		}
	}

	heatmapData := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"page_heatmaps": aggregatedHeatmaps,
			"scroll_depth":  scrollStats,
			"date_range": map[string]string{
				"start": startDate,
				"end":   endDate,
			},
			"total_events": len(events),
		},
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
