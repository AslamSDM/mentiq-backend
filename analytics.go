package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-gonic/gin"
)

// Analytics data structures
type AnalyticsQuery struct {
	StartDate  string   `json:"start_date" form:"start_date"`
	EndDate    string   `json:"end_date" form:"end_date"`
	EventTypes []string `json:"event_types" form:"event_types"`
	UserID     string   `json:"user_id" form:"user_id"`
	Metrics    []string `json:"metrics" form:"metrics"`
	GroupBy    string   `json:"group_by" form:"group_by"` // hour, day, week, month
}

type MetricResult struct {
	Metric     string                 `json:"metric"`
	Value      interface{}            `json:"value"`
	Breakdown  map[string]interface{} `json:"breakdown,omitempty"`
	TimeSeries []TimeSeriesPoint      `json:"time_series,omitempty"`
}

type TimeSeriesPoint struct {
	Timestamp time.Time   `json:"timestamp"`
	Value     interface{} `json:"value"`
}

type AnalyticsResponse struct {
	Query   AnalyticsQuery `json:"query"`
	Results []MetricResult `json:"results"`
	Meta    struct {
		TotalEvents    int           `json:"total_events"`
		ProcessingTime time.Duration `json:"processing_time_ms"`
		DateRange      string        `json:"date_range"`
	} `json:"meta"`
}

// Core analytics functions
func (as *AnalyticsService) GetAnalyticsHandler(c *gin.Context) {
	start := time.Now()

	var query AnalyticsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query parameters"})
		return
	}

	// Get account and project from context
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Set default values
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

	// Fetch and process events
	events, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), query.StartDate, query.EndDate)
	if err != nil {
		log.Printf("Error fetching events: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	// Filter events based on query
	filteredEvents := as.filterEvents(events, query)

	// Calculate metrics
	results := as.calculateMetrics(filteredEvents, query)

	response := AnalyticsResponse{
		Query:   query,
		Results: results,
	}
	response.Meta.TotalEvents = len(filteredEvents)
	response.Meta.ProcessingTime = time.Since(start)
	response.Meta.DateRange = fmt.Sprintf("%s to %s", query.StartDate, query.EndDate)

	c.JSON(http.StatusOK, response)
}

func (as *AnalyticsService) fetchEventsForDateRange(accountID, projectID, startDate, endDate string) ([]Event, error) {
	// Check cache first
	if cachedEvents, found := as.getCachedEvents(accountID, projectID, startDate, endDate); found {
		log.Printf("Cache HIT: Found %d events for account=%s, project=%s, from %s to %s", 
			len(cachedEvents), accountID, projectID, startDate, endDate)
		return cachedEvents, nil
	}

	log.Printf("Cache MISS: Fetching events for account=%s, project=%s, from %s to %s", 
		accountID, projectID, startDate, endDate)

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, err
	}
	
	// Use a single prefix to get all events for the account/project
	prefix := fmt.Sprintf("events/account_id=%s/project_id=%s/", accountID, projectID)
	
	// Get all events at once with optimized fetching
	allEvents, err := as.fetchEventsFromS3Optimized(prefix, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch events: %v", err)
	}

	log.Printf("Found %d events in date range, caching for future requests", len(allEvents))
	
	// Cache the results for future requests
	as.setCachedEvents(accountID, projectID, startDate, endDate, allEvents)

	return allEvents, nil
}

// fetchEventsFromS3Optimized fetches all events with parallel processing and date filtering
func (as *AnalyticsService) fetchEventsFromS3Optimized(prefix string, startDate, endDate time.Time) ([]Event, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(as.bucketName),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int64(1000), // Limit to avoid memory issues
	}

	var allObjectKeys []string
	
	// First, get all object keys
	err := as.s3Client.ListObjectsV2Pages(input, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			// Quick date filter based on object key
			if as.isObjectInDateRange(*obj.Key, startDate, endDate) {
				allObjectKeys = append(allObjectKeys, *obj.Key)
			}
		}
		return true // Continue to next page
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %v", err)
	}

	log.Printf("Found %d relevant objects to fetch", len(allObjectKeys))

	// Fetch events in parallel batches
	return as.fetchEventsInParallel(allObjectKeys, 50) // Process 50 events concurrently
}

// isObjectInDateRange quickly checks if an S3 object is in the date range based on its key
func (as *AnalyticsService) isObjectInDateRange(key string, startDate, endDate time.Time) bool {
	// Extract date from key: events/account_id=xxx/project_id=xxx/year=2025/month=09/day=22/event.json
	parts := strings.Split(key, "/")
	
	var year, month, day int
	for _, part := range parts {
		if strings.HasPrefix(part, "year=") {
			year, _ = strconv.Atoi(strings.TrimPrefix(part, "year="))
		} else if strings.HasPrefix(part, "month=") {
			month, _ = strconv.Atoi(strings.TrimPrefix(part, "month="))
		} else if strings.HasPrefix(part, "day=") {
			day, _ = strconv.Atoi(strings.TrimPrefix(part, "day="))
		}
	}
	
	if year == 0 || month == 0 || day == 0 {
		return true // If we can't parse date, include it
	}
	
	eventDate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return !eventDate.Before(startDate) && !eventDate.After(endDate)
}

// fetchEventsInParallel fetches multiple events concurrently
func (as *AnalyticsService) fetchEventsInParallel(keys []string, maxConcurrency int) ([]Event, error) {
	if len(keys) == 0 {
		return []Event{}, nil
	}

	// Create channels for coordination
	keyChan := make(chan string, len(keys))
	resultChan := make(chan []Event, len(keys))
	errorChan := make(chan error, len(keys))

	// Send all keys to the channel
	for _, key := range keys {
		keyChan <- key
	}
	close(keyChan)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrency && i < len(keys); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range keyChan {
				events, err := as.fetchSingleEventOptimized(key)
				if err != nil {
					errorChan <- fmt.Errorf("failed to fetch %s: %v", key, err)
					continue
				}
				resultChan <- events
			}
		}()
	}

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(resultChan)
		close(errorChan)
	}()

	// Collect results
	var allEvents []Event
	var errors []string

	for {
		select {
		case events, ok := <-resultChan:
			if !ok {
				resultChan = nil
			} else {
				allEvents = append(allEvents, events...)
			}
		case err, ok := <-errorChan:
			if !ok {
				errorChan = nil
			} else {
				errors = append(errors, err.Error())
			}
		}

		if resultChan == nil && errorChan == nil {
			break
		}
	}

	// Log errors but don't fail the entire operation
	if len(errors) > 0 {
		log.Printf("Warning: %d events failed to fetch: %v", len(errors), errors[:min(5, len(errors))])
	}

	log.Printf("Successfully fetched %d events", len(allEvents))
	return allEvents, nil
}

// fetchSingleEventOptimized fetches a single event or batch with better error handling
func (as *AnalyticsService) fetchSingleEventOptimized(key string) ([]Event, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(as.bucketName),
		Key:    aws.String(key),
	}

	result, err := as.s3Client.GetObject(input)
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()

	// Check if this is a batch file
	if strings.Contains(key, "batch_") {
		// Handle batch file
		var batchPayload struct {
			BatchID    string  `json:"batch_id"`
			BatchSize  int     `json:"batch_size"`
			UploadedAt string  `json:"uploaded_at"`
			Events     []Event `json:"events"`
		}
		
		err = json.NewDecoder(result.Body).Decode(&batchPayload)
		if err != nil {
			return nil, fmt.Errorf("failed to decode batch file: %v", err)
		}
		
		return batchPayload.Events, nil
	} else {
		// Handle single event file
		var event Event
		err = json.NewDecoder(result.Body).Decode(&event)
		if err != nil {
			return nil, err
		}
		
		return []Event{event}, nil
	}
}


// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (as *AnalyticsService) fetchEventsFromS3(prefix string) ([]Event, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(as.bucketName),
		Prefix: aws.String(prefix),
	}

	var events []Event

	err := as.s3Client.ListObjectsV2Pages(input, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			eventsBatch, err := as.fetchSingleEventOptimized(*obj.Key)
			if err != nil {
				log.Printf("Warning: Could not fetch event %s: %v", *obj.Key, err)
				continue
			}
			events = append(events, eventsBatch...)
		}
		return true
	})

	return events, err
}

func (as *AnalyticsService) fetchSingleEvent(key string) (Event, error) {
	// Use the optimized version and return the first event
	events, err := as.fetchSingleEventOptimized(key)
	if err != nil {
		return Event{}, err
	}
	if len(events) == 0 {
		return Event{}, fmt.Errorf("no events found in file %s", key)
	}
	return events[0], nil
}

func (as *AnalyticsService) filterEvents(events []Event, query AnalyticsQuery) []Event {
	var filtered []Event

	for _, event := range events {
		// Filter by event types
		if len(query.EventTypes) > 0 {
			found := false
			for _, eventType := range query.EventTypes {
				if event.EventType == eventType {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by user ID
		if query.UserID != "" && event.UserID != query.UserID {
			continue
		}

		filtered = append(filtered, event)
	}

	return filtered
}

func (as *AnalyticsService) calculateMetrics(events []Event, query AnalyticsQuery) []MetricResult {
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
			// Calculate bounce rate (sessions with only one event)
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
			// Daily Active Users - unique users in the current day
			today := time.Now().UTC().Format("2006-01-02")
			todayUsers := make(map[string]bool)
			
			for _, event := range events {
				eventDate := event.Timestamp.Format("2006-01-02")
				if eventDate == today && event.UserID != "" {
					todayUsers[event.UserID] = true
				}
			}

			results = append(results, MetricResult{
				Metric: "dau",
				Value:  len(todayUsers),
				TimeSeries: as.getDAUTimeSeries(events, query.GroupBy),
			})

		case "wau":
			// Weekly Active Users - unique users in the last 7 days
			sevenDaysAgo := time.Now().UTC().AddDate(0, 0, -7)
			weeklyUsers := make(map[string]bool)
			
			for _, event := range events {
				if event.Timestamp.After(sevenDaysAgo) && event.UserID != "" {
					weeklyUsers[event.UserID] = true
				}
			}

			results = append(results, MetricResult{
				Metric: "wau",
				Value:  len(weeklyUsers),
				TimeSeries: as.getWAUTimeSeries(events, query.GroupBy),
			})

		case "mau":
			// Monthly Active Users - unique users in the last 30 days
			thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)
			monthlyUsers := make(map[string]bool)
			
			for _, event := range events {
				if event.Timestamp.After(thirtyDaysAgo) && event.UserID != "" {
					monthlyUsers[event.UserID] = true
				}
			}

			results = append(results, MetricResult{
				Metric: "mau",
				Value:  len(monthlyUsers),
				TimeSeries: as.getMAUTimeSeries(events, query.GroupBy),
			})

		case "page_views":
			// Count page view events
			pageViews := 0
			pageViewsByPath := make(map[string]int)
			
			for _, event := range events {
				if event.EventType == "page_view" || event.EventType == "pageview" {
					pageViews++
					
					// Extract path from properties if available
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
				Metric: "page_views",
				Value:  pageViews,
				Breakdown: convertMapToInterface(pageViewsByPath),
				TimeSeries: as.getPageViewTimeSeries(events, query.GroupBy),
			})
		}
	}

	return results
}

func (as *AnalyticsService) getTimeSeriesData(events []Event, groupBy string, calculator func([]Event) interface{}) []TimeSeriesPoint {
	groupedEvents := make(map[string][]Event)

	for _, event := range events {
		var key string
		switch groupBy {
		case "hour":
			key = event.Timestamp.Format("2006-01-02 15")
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
		groupedEvents[key] = append(groupedEvents[key], event)
	}

	var timeSeries []TimeSeriesPoint
	for key, eventGroup := range groupedEvents {
		timestamp, _ := parseTimeByGroupBy(key, groupBy)
		timeSeries = append(timeSeries, TimeSeriesPoint{
			Timestamp: timestamp,
			Value:     calculator(eventGroup),
		})
	}

	// Sort by timestamp
	sort.Slice(timeSeries, func(i, j int) bool {
		return timeSeries[i].Timestamp.Before(timeSeries[j].Timestamp)
	})

	return timeSeries
}

func parseTimeByGroupBy(key, groupBy string) (time.Time, error) {
	switch groupBy {
	case "hour":
		return time.Parse("2006-01-02 15", key)
	case "day":
		return time.Parse("2006-01-02", key)
	case "week":
		parts := strings.Split(key, "-W")
		year, _ := strconv.Atoi(parts[0])
		week, _ := strconv.Atoi(parts[1])
		return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, (week-1)*7), nil
	case "month":
		return time.Parse("2006-01", key)
	default:
		return time.Parse("2006-01-02", key)
	}
}

func convertMapToInterface(m map[string]int) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}

// getDAUTimeSeries calculates daily active users over time
func (as *AnalyticsService) getDAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	groupedUsers := make(map[string]map[string]bool)

	for _, event := range events {
		if event.UserID == "" {
			continue
		}

		var key string
		switch groupBy {
		case "hour":
			key = event.Timestamp.Format("2006-01-02 15")
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

		if groupedUsers[key] == nil {
			groupedUsers[key] = make(map[string]bool)
		}
		groupedUsers[key][event.UserID] = true
	}

	var timeSeries []TimeSeriesPoint
	for key, users := range groupedUsers {
		timestamp, _ := parseTimeByGroupBy(key, groupBy)
		timeSeries = append(timeSeries, TimeSeriesPoint{
			Timestamp: timestamp,
			Value:     len(users),
		})
	}

	sort.Slice(timeSeries, func(i, j int) bool {
		return timeSeries[i].Timestamp.Before(timeSeries[j].Timestamp)
	})

	return timeSeries
}

// getWAUTimeSeries calculates weekly active users over time
func (as *AnalyticsService) getWAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	groupedData := make(map[string][]Event)

	for _, event := range events {
		var key string
		switch groupBy {
		case "week":
			year, week := event.Timestamp.ISOWeek()
			key = fmt.Sprintf("%d-W%02d", year, week)
		case "month":
			key = event.Timestamp.Format("2006-01")
		default:
			key = event.Timestamp.Format("2006-01-02")
		}
		groupedData[key] = append(groupedData[key], event)
	}

	var timeSeries []TimeSeriesPoint
	for key := range groupedData {
		timestamp, _ := parseTimeByGroupBy(key, groupBy)
		
		// For each time period, get unique users from the last 7 days
		weekStart := timestamp.AddDate(0, 0, -7)
		weeklyUsers := make(map[string]bool)
		
		for _, event := range events {
			if event.Timestamp.After(weekStart) && event.Timestamp.Before(timestamp.AddDate(0, 0, 1)) && event.UserID != "" {
				weeklyUsers[event.UserID] = true
			}
		}
		
		timeSeries = append(timeSeries, TimeSeriesPoint{
			Timestamp: timestamp,
			Value:     len(weeklyUsers),
		})
	}

	sort.Slice(timeSeries, func(i, j int) bool {
		return timeSeries[i].Timestamp.Before(timeSeries[j].Timestamp)
	})

	return timeSeries
}

// getMAUTimeSeries calculates monthly active users over time
func (as *AnalyticsService) getMAUTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	groupedData := make(map[string][]Event)

	for _, event := range events {
		var key string
		switch groupBy {
		case "month":
			key = event.Timestamp.Format("2006-01")
		default:
			key = event.Timestamp.Format("2006-01-02")
		}
		groupedData[key] = append(groupedData[key], event)
	}

	var timeSeries []TimeSeriesPoint
	for key := range groupedData {
		timestamp, _ := parseTimeByGroupBy(key, groupBy)
		
		// For each time period, get unique users from the last 30 days
		monthStart := timestamp.AddDate(0, 0, -30)
		monthlyUsers := make(map[string]bool)
		
		for _, event := range events {
			if event.Timestamp.After(monthStart) && event.Timestamp.Before(timestamp.AddDate(0, 0, 1)) && event.UserID != "" {
				monthlyUsers[event.UserID] = true
			}
		}
		
		timeSeries = append(timeSeries, TimeSeriesPoint{
			Timestamp: timestamp,
			Value:     len(monthlyUsers),
		})
	}

	sort.Slice(timeSeries, func(i, j int) bool {
		return timeSeries[i].Timestamp.Before(timeSeries[j].Timestamp)
	})

	return timeSeries
}

// getPageViewTimeSeries calculates page views over time
func (as *AnalyticsService) getPageViewTimeSeries(events []Event, groupBy string) []TimeSeriesPoint {
	groupedPageViews := make(map[string]int)

	for _, event := range events {
		if event.EventType != "page_view" && event.EventType != "pageview" {
			continue
		}

		var key string
		switch groupBy {
		case "hour":
			key = event.Timestamp.Format("2006-01-02 15")
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

		groupedPageViews[key]++
	}

	var timeSeries []TimeSeriesPoint
	for key, count := range groupedPageViews {
		timestamp, _ := parseTimeByGroupBy(key, groupBy)
		timeSeries = append(timeSeries, TimeSeriesPoint{
			Timestamp: timestamp,
			Value:     count,
		})
	}

	sort.Slice(timeSeries, func(i, j int) bool {
		return timeSeries[i].Timestamp.Before(timeSeries[j].Timestamp)
	})

	return timeSeries
}

// Dashboard endpoints
func (as *AnalyticsService) GetDashboardHandler(c *gin.Context) {
	// Get account and project from context
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	log.Printf("AccountID: %v, ProjectID: %v", accountID, projectID)
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Check cache first
	if cachedDashboard, found := as.getCachedDashboard(accountID.(string), projectID.(string)); found {
		log.Printf("Cache HIT: Returning cached dashboard data")
		c.JSON(http.StatusOK, cachedDashboard)
		return
	}

	log.Printf("Cache MISS: Calculating dashboard data")

	// Get recent events for dashboard calculations
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02") // Last 30 days
	
	events, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), startDate, endDate)
	log.Printf("Fetched %d events for dashboard", len(events))
	if err != nil {
		log.Printf("Error fetching events for dashboard: %v", err)
		// Return empty dashboard if we can't fetch events
		events = []Event{}
	}

	// Calculate dashboard data
	dashboardData := as.calculateDashboardData(events)
	
	// Cache the results
	as.setCachedDashboard(accountID.(string), projectID.(string), dashboardData)
	
	log.Printf("Dashboard data calculated and cached")
	c.JSON(http.StatusOK, dashboardData)
}

// calculateDashboardData extracts dashboard calculation logic for reuse
func (as *AnalyticsService) calculateDashboardData(events []Event) map[string]interface{} {
	// Calculate metrics
	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1)
	sevenDaysAgo := today.AddDate(0, 0, -7)
	thirtyDaysAgo := today.AddDate(0, 0, -30)
	
	log.Printf("Calculating dashboard metrics for %d events", len(events))
	
	// Calculate DAU, WAU, MAU
	todayUsers := make(map[string]bool)
	yesterdayUsers := make(map[string]bool)
	weeklyUsers := make(map[string]bool)
	monthlyUsers := make(map[string]bool)
	
	// Page views
	pageViewsToday := 0
	pageViewsYesterday := 0
	totalPageViews := 0
	
	// Event counts
	eventsToday := 0
	eventsYesterday := 0

	for _, event := range events {
		eventDate := event.Timestamp.Format("2006-01-02")
		todayDate := today.Format("2006-01-02")
		yesterdayDate := yesterday.Format("2006-01-02")

		// Count events
		if eventDate == todayDate {
			eventsToday++
		} else if eventDate == yesterdayDate {
			eventsYesterday++
		}

		// Count page views
		if event.EventType == "page_view" || event.EventType == "pageview" {
			totalPageViews++
			if eventDate == todayDate {
				pageViewsToday++
			} else if eventDate == yesterdayDate {
				pageViewsYesterday++
			}
		}

		// Count unique users
		if event.UserID != "" {
			if eventDate == todayDate {
				todayUsers[event.UserID] = true
			} else if eventDate == yesterdayDate {
				yesterdayUsers[event.UserID] = true
			}
			
			if event.Timestamp.After(sevenDaysAgo) {
				weeklyUsers[event.UserID] = true
			}
			
			if event.Timestamp.After(thirtyDaysAgo) {
				monthlyUsers[event.UserID] = true
			}
		}
	}

	// Calculate growth rates
	var eventGrowthRate string
	if eventsYesterday > 0 {
		growth := ((float64(eventsToday) - float64(eventsYesterday)) / float64(eventsYesterday)) * 100
		eventGrowthRate = fmt.Sprintf("%.1f%%", growth)
	} else {
		eventGrowthRate = "N/A"
	}

	var userGrowthRate string
	if len(yesterdayUsers) > 0 {
		growth := ((float64(len(todayUsers)) - float64(len(yesterdayUsers))) / float64(len(yesterdayUsers))) * 100
		userGrowthRate = fmt.Sprintf("%.1f%%", growth)
	} else {
		userGrowthRate = "N/A"
	}

	dashboardData := map[string]interface{}{
		"overview": map[string]interface{}{
			"total_events_today":     eventsToday,
			"total_events_yesterday": eventsYesterday,
			"unique_users_today":     len(todayUsers),
			"unique_users_yesterday": len(yesterdayUsers),
			"event_growth_rate":      eventGrowthRate,
			"user_growth_rate":       userGrowthRate,
		},
		"user_metrics": map[string]interface{}{
			"dau": len(todayUsers),
			"wau": len(weeklyUsers),
			"mau": len(monthlyUsers),
		},
		"page_metrics": map[string]interface{}{
			"page_views_today":     pageViewsToday,
			"page_views_yesterday": pageViewsYesterday,
			"total_page_views":     totalPageViews,
		},
		"user_activity": map[string]interface{}{
			"active_users_last_7_days":  len(weeklyUsers),
			"active_users_last_30_days": len(monthlyUsers),
			"new_users_today":           len(todayUsers),
		},
		"performance": map[string]interface{}{
			"avg_session_duration": "0s", // Would need session calculation
			"bounce_rate":          "0%", // Would need session calculation
			"pages_per_session":    0,    // Would need session calculation
		},
	}

	return dashboardData
}

// GetUserMetricsHandler provides detailed user metrics (DAU, WAU, MAU)
func (as *AnalyticsService) GetUserMetricsHandler(c *gin.Context) {
	// Get account and project from context
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get date range from query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	groupBy := c.DefaultQuery("group_by", "day")

	// Fetch events for the specified date range
	events, err := as.fetchEventsForDateRange(accountID.(string), projectID.(string), startDate, endDate)
	if err != nil {
		log.Printf("Error fetching events for user metrics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch events"})
		return
	}

	// Calculate current metrics
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	sevenDaysAgo := now.AddDate(0, 0, -7)
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	dauUsers := make(map[string]bool)
	wauUsers := make(map[string]bool)
	mauUsers := make(map[string]bool)

	for _, event := range events {
		if event.UserID == "" {
			continue
		}

		eventDate := event.Timestamp.Format("2006-01-02")
		if eventDate == today {
			dauUsers[event.UserID] = true
		}

		if event.Timestamp.After(sevenDaysAgo) {
			wauUsers[event.UserID] = true
		}

		if event.Timestamp.After(thirtyDaysAgo) {
			mauUsers[event.UserID] = true
		}
	}

	// Generate time series data
	dauTimeSeries := as.getDAUTimeSeries(events, groupBy)
	wauTimeSeries := as.getWAUTimeSeries(events, groupBy)
	mauTimeSeries := as.getMAUTimeSeries(events, groupBy)

	response := map[string]interface{}{
		"current_metrics": map[string]interface{}{
			"dau": len(dauUsers),
			"wau": len(wauUsers),
			"mau": len(mauUsers),
		},
		"time_series": map[string]interface{}{
			"dau": dauTimeSeries,
			"wau": wauTimeSeries,
			"mau": mauTimeSeries,
		},
		"date_range": map[string]string{
			"start_date": startDate,
			"end_date":   endDate,
			"group_by":   groupBy,
		},
	}

	c.JSON(http.StatusOK, response)
}

// Real-time analytics endpoint
func (as *AnalyticsService) GetRealTimeHandler(c *gin.Context) {
	// Get account and project from context
	accountID, _ := c.Get("account_id")
	projectID, _ := c.Get("project_id")
	if accountID == "" || projectID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get events from last hour for real-time view
	now := time.Now()

	// In a real implementation, you might want to cache this data
	// or use a different storage mechanism for real-time data

	realTimeData := map[string]interface{}{
		"active_users_now":   0,
		"events_last_hour":   0,
		"events_last_minute": 0,
		"top_pages_now":      []map[string]interface{}{},
		"recent_events":      []Event{},
		"timestamp":          now,
	}

	c.JSON(http.StatusOK, realTimeData)
}
