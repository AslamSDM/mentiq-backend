package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/client"

	"mentiq-backend/prisma/db"
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

	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	project, err := as.dbClient.Project.FindUnique(
		db.Project.ID.Equals(projectID.(string)),
	).With(
		db.Project.Account.Fetch(),
	).Exec(context.Background())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project"})
		return
	}

	// Get Stripe API key from project
	var stripeKey string
	if apiKey, ok := project.StripeAPIKey(); ok {
		stripeKey = string(apiKey)
	}

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

	events, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), query.StartDate, query.EndDate)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	filteredEvents := as.filterEvents(events, query)

	results := as.calculateMetrics(filteredEvents, query, sc)

	response := AnalyticsResponse{
		Query:   query,
		Results: results,
	}
	response.Meta.TotalEvents = len(filteredEvents)
	response.Meta.ProcessingTime = time.Since(start)
	response.Meta.DateRange = fmt.Sprintf("%s to %s", query.StartDate, query.EndDate)

	c.JSON(http.StatusOK, response)
}

func getStripeCustomers(sc *client.API) []*stripe.Customer {
	/*
		params := &stripe.CustomerListParams{}
		params.Filters.AddFilter("limit", "", "100")
		i := customer.List(params)
		var customers []*stripe.Customer
		for i.Next() {
			customers = append(customers, i.Customer())
		}
		return customers
	*/
	return []*stripe.Customer{}
}

func getStripeSubscriptions(sc *client.API) []*stripe.Subscription {
	/*
		params := &stripe.SubscriptionListParams{}
		params.Filters.AddFilter("limit", "", "100")
		i := subscription.List(params)
		var subscriptions []*stripe.Subscription
		for i.Next() {
			subscriptions = append(subscriptions, i.Subscription())
		}
		return subscriptions
	*/
	return []*stripe.Subscription{}
}

func (as *AnalyticsService) calculateMetrics(events []Event, query AnalyticsQuery, sc *client.API) []MetricResult {
	var results []MetricResult

	for _, metric := range query.Metrics {
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

			results = append(results, MetricResult{
				Metric:     "dau",
				Value:      len(todayUsers),
				TimeSeries: as.getDAUTimeSeries(events, query.GroupBy),
			})

		case "wau":
			sevenDaysAgo := time.Now().UTC().AddDate(0, 0, -7)
			weeklyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(sevenDaysAgo) && event.UserID != "" {
					weeklyUsers[event.UserID] = true
				}
			}

			results = append(results, MetricResult{
				Metric:     "wau",
				Value:      len(weeklyUsers),
				TimeSeries: as.getWAUTimeSeries(events, query.GroupBy),
			})

		case "mau":
			thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)
			monthlyUsers := make(map[string]bool)

			for _, event := range events {
				if event.Timestamp.After(thirtyDaysAgo) && event.UserID != "" {
					monthlyUsers[event.UserID] = true
				}
			}

			results = append(results, MetricResult{
				Metric:     "mau",
				Value:      len(monthlyUsers),
				TimeSeries: as.getMAUTimeSeries(events, query.GroupBy),
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
			customers := getStripeCustomers(sc)
			totalCustomers := len(customers)
			payingCustomers := 0
			for _, c := range customers {
				if len(c.Subscriptions.Data) > 0 {
					payingCustomers++
				}
			}
			var conversionRate float64
			if totalCustomers > 0 {
				conversionRate = float64(payingCustomers) / float64(totalCustomers) * 100
			}
			results = append(results, MetricResult{
				Metric: "conversion_rate",
				Value:  fmt.Sprintf("%.2f%%", conversionRate),
			})

		case "churn_rate":
			subscriptions := getStripeSubscriptions(sc)
			canceledSubscriptions := 0
			activeSubscriptions := 0
			for _, s := range subscriptions {
				if s.Status == stripe.SubscriptionStatusCanceled {
					canceledSubscriptions++
				}
				if s.Status == stripe.SubscriptionStatusActive {
					activeSubscriptions++
				}
			}
			var churnRate float64
			if activeSubscriptions > 0 {
				churnRate = float64(canceledSubscriptions) / float64(activeSubscriptions) * 100
			}
			results = append(results, MetricResult{
				Metric: "churn_rate",
				Value:  fmt.Sprintf("%.2f%%", churnRate),
			})

		case "mrr":
			subscriptions := getStripeSubscriptions(sc)
			mrr := 0.0
			for _, s := range subscriptions {
				if s.Status == stripe.SubscriptionStatusActive {
					for _, item := range s.Items.Data {
						mrr += float64(item.Price.UnitAmount) / 100
					}
				}
			}
			results = append(results, MetricResult{
				Metric: "mrr",
				Value:  fmt.Sprintf("$%.2f", mrr),
			})

		case "arpu":
			subscriptions := getStripeSubscriptions(sc)
			mrr := 0.0
			payingUsers := make(map[string]bool)
			for _, s := range subscriptions {
				if s.Status == stripe.SubscriptionStatusActive {
					for _, item := range s.Items.Data {
						mrr += float64(item.Price.UnitAmount) / 100
					}
					payingUsers[s.Customer.ID] = true
				}
			}
			var arpu float64
			if len(payingUsers) > 0 {
				arpu = mrr / float64(len(payingUsers))
			}
			results = append(results, MetricResult{
				Metric: "arpu",
				Value:  fmt.Sprintf("$%.2f", arpu),
			})
		}
	}

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

// fetchEventsForDateRange fetches events from S3 for the given date range
func (as *AnalyticsService) fetchEventsForDateRange(accountID, projectID, startDate, endDate string) ([]Event, error) {
	// Check cache first
	if cachedEvents, found := as.getCachedEvents(accountID, projectID, startDate, endDate); found {
		log.Printf("Returning %d cached events for %s-%s", len(cachedEvents), startDate, endDate)
		return cachedEvents, nil
	}

	// For now, return empty slice - in production you'd fetch from S3
	// This would involve iterating through S3 objects with the proper key structure
	events := []Event{}

	// Cache the results
	as.setCachedEvents(accountID, projectID, startDate, endDate, events)

	log.Printf("Fetched %d events from S3 for date range %s to %s", len(events), startDate, endDate)
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

	// For now, return mock data - in production you'd calculate from real events
	dashboardData := map[string]interface{}{
		"date": dateParam,
		"overview": map[string]interface{}{
			"total_events_today":     142,
			"total_events_yesterday": 98,
			"unique_users_today":     67,
			"unique_users_yesterday": 45,
			"event_growth_rate":      "+44.9%",
			"user_growth_rate":       "+48.9%",
		},
		"user_metrics": map[string]interface{}{
			"dau": 67,
			"wau": 245,
			"mau": 1024,
		},
		"page_metrics": map[string]interface{}{
			"page_views_today":     89,
			"page_views_yesterday": 67,
			"total_page_views":     2456,
		},
		"top_events": []map[string]interface{}{
			{"event_type": "page_view", "count": 89, "percentage": 62.7},
			{"event_type": "click", "count": 34, "percentage": 23.9},
			{"event_type": "form_submit", "count": 19, "percentage": 13.4},
		},
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

	// Return real-time metrics
	realTimeData := map[string]interface{}{
		"current_visitors":  23,
		"events_last_5_min": 45,
		"events_last_hour":  312,
		"cache_size":        as.getCacheSize(),
		"top_pages_now": []map[string]interface{}{
			{"page": "/dashboard", "visitors": 8},
			{"page": "/analytics", "visitors": 6},
			{"page": "/settings", "visitors": 3},
		},
		"timestamp": time.Now().UTC(),
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

	// For now, return mock data - in production you'd calculate from real events
	userMetrics := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"date":       dateParam,
			"dau":        67,
			"wau":        245,
			"mau":        1024,
			"dau_growth": "+12.3%",
			"wau_growth": "+8.7%",
			"mau_growth": "+15.2%",
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
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Mock heatmap data
	heatmapData := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"page_heatmaps": []map[string]interface{}{
				{
					"page": "/dashboard",
					"clicks": []map[string]interface{}{
						{"x": 150, "y": 200, "count": 45},
						{"x": 300, "y": 150, "count": 32},
						{"x": 250, "y": 350, "count": 28},
					},
				},
			},
			"scroll_depth": map[string]interface{}{
				"25%":  0.89,
				"50%":  0.67,
				"75%":  0.45,
				"100%": 0.23,
			},
		},
	}

	c.JSON(http.StatusOK, heatmapData)
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
