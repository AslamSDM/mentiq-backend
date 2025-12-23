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
	"gorm.io/gorm"
)

// EnhancedAnalyticsService handles advanced analytics calculations
type EnhancedAnalyticsService struct {
	db *gorm.DB
}

// NewEnhancedAnalyticsService creates a new enhanced analytics service
func NewEnhancedAnalyticsService(db *gorm.DB) *EnhancedAnalyticsService {
	return &EnhancedAnalyticsService{
		db: db,
	}
}

// validateProjectAccess validates authentication and project ownership
func (eas *EnhancedAnalyticsService) validateProjectAccess(c *gin.Context) (string, string, error) {
	// Validate authentication
	accountID, exists := c.Get("account_id")
	if !exists || accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized - missing account_id"})
		return "", "", gin.Error{Err: nil, Type: gin.ErrorTypePublic}
	}

	// Get and validate project access
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing project_id parameter"})
		return "", "", gin.Error{Err: nil, Type: gin.ErrorTypePublic}
	}

	// Verify project ownership
	var project Project
	if err := eas.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "Project not found or access denied"})
		return "", "", gin.Error{Err: nil, Type: gin.ErrorTypePublic}
	}

	return accountID.(string), projectID, nil
}

// parseDateRange parses and validates start and end date parameters
func (eas *EnhancedAnalyticsService) parseDateRange(c *gin.Context) (time.Time, time.Time, error) {
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid start_date format. Use YYYY-MM-DD"})
		return time.Time{}, time.Time{}, err
	}

	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid end_date format. Use YYYY-MM-DD"})
		return time.Time{}, time.Time{}, err
	}

	// Ensure start date is not after end date
	if start.After(end) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date cannot be after end_date"})
		return time.Time{}, time.Time{}, gin.Error{Err: nil, Type: gin.ErrorTypePublic}
	}

	return start, end, nil
}

// LocationAnalyticsHandler provides geographical analytics
func (eas *EnhancedAnalyticsService) LocationAnalyticsHandler(c *gin.Context) {
	// Validate authentication and project access
	_, projectID, err := eas.validateProjectAccess(c)
	if err != nil {
		return // Response already sent by validateProjectAccess
	}

	// Parse and validate date range
	start, end, err := eas.parseDateRange(c)
	if err != nil {
		return // Response already sent by parseDateRange
	}

	locationData, err := eas.calculateLocationAnalytics(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": start.Format("2006-01-02"),
		"end_date":   end.Format("2006-01-02"),
		"data":       locationData,
	})
}

// DeviceAnalyticsHandler provides device and platform analytics
func (eas *EnhancedAnalyticsService) DeviceAnalyticsHandler(c *gin.Context) {
	// Validate authentication and project access
	_, projectID, err := eas.validateProjectAccess(c)
	if err != nil {
		return // Response already sent by validateProjectAccess
	}

	// Parse and validate date range
	start, end, err := eas.parseDateRange(c)
	if err != nil {
		return // Response already sent by parseDateRange
	}

	deviceData, err := eas.calculateDeviceAnalytics(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": start.Format("2006-01-02"),
		"end_date":   end.Format("2006-01-02"),
		"data":       deviceData,
	})
}

// RetentionCohortHandler provides cohort retention analysis
func (eas *EnhancedAnalyticsService) RetentionCohortHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Parse date range
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	cohortData, err := eas.calculateRetentionCohorts(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": startDate,
		"end_date":   endDate,
		"cohorts":    cohortData,
	})
}

// FeatureAdoptionHandler provides feature adoption analytics
func (eas *EnhancedAnalyticsService) FeatureAdoptionHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	adoptionData, err := eas.calculateFeatureAdoption(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": startDate,
		"end_date":   endDate,
		"features":   adoptionData,
	})
}

// ChurnRiskHandler provides churn risk analysis
func (eas *EnhancedAnalyticsService) ChurnRiskHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	riskThreshold := c.DefaultQuery("risk_threshold", "70") // Default to 70% risk threshold

	threshold, _ := strconv.ParseFloat(riskThreshold, 64)
	churnData, err := eas.calculateChurnRisk(projectID, threshold)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": gin.H{
			"at_risk_users": churnData["at_risk_users"],
			"total_at_risk": churnData["churn_stats"].(map[string]interface{})["at_risk_users"],
			"churn_rate":    churnData["churn_stats"].(map[string]interface{})["churn_rate_30d"],
		},
	})
}

// ConversionFunnelHandler provides conversion funnel analysis
func (eas *EnhancedAnalyticsService) ConversionFunnelHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	funnelName := c.DefaultQuery("funnel_name", "default")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	funnelData, err := eas.calculateConversionFunnel(projectID, funnelName, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id":   projectID,
		"funnel_name":  funnelName,
		"start_date":   startDate,
		"end_date":     endDate,
		"funnel_steps": funnelData,
	})
}

// Enhanced Session Analytics Handler
func (eas *EnhancedAnalyticsService) SessionAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	sessionData, err := eas.calculateSessionMetrics(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id":   projectID,
		"start_date":   startDate,
		"end_date":     endDate,
		"session_data": sessionData,
	})
}

// calculateLocationAnalytics calculates geographical analytics from events
func (eas *EnhancedAnalyticsService) calculateLocationAnalytics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Use on-the-fly aggregation from events table
	type LocationResult struct {
		Country   string
		City      string
		Sessions  int
		Users     int
		PageViews int
	}

	var results []LocationResult

	// Adjust end date to include the full day
	endOfDay := end.Add(24*time.Hour - time.Nanosecond)

	err := eas.db.Model(&Event{}).
		Select("country, city, COUNT(DISTINCT session_id) as sessions, COUNT(DISTINCT user_id) as users, SUM(CASE WHEN event_type IN ('page_view', 'pageview') THEN 1 ELSE 0 END) as page_views").
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, start, endOfDay).
		Group("country, city").
		Scan(&results).Error

	if err != nil {
		log.Printf("Error calculating location analytics for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to calculate location analytics: %v", err)
	}

	log.Printf("Calculated location analytics from %d records for project %s", len(results), projectID)

	// Aggregate by country
	countryMap := make(map[string]map[string]interface{})
	cityMap := make(map[string]map[string]interface{})

	for _, res := range results {
		if res.Country == "" {
			res.Country = "Unknown"
		}
		if res.City == "" {
			res.City = "Unknown"
		}

		// Country aggregation
		if _, exists := countryMap[res.Country]; !exists {
			countryMap[res.Country] = map[string]interface{}{
				"country":         res.Country,
				"country_code":    "", // We don't have code in events yet
				"sessions":        0,
				"users":           0,
				"page_views":      0,
				"bounce_rate":     "0.00%", // Placeholder
				"conversion_rate": "0.00%", // Placeholder
				"revenue":         0.0,
			}
		}
		cEntry := countryMap[res.Country]
		cEntry["sessions"] = cEntry["sessions"].(int) + res.Sessions
		cEntry["users"] = cEntry["users"].(int) + res.Users
		cEntry["page_views"] = cEntry["page_views"].(int) + res.PageViews

		// City aggregation
		cityKey := res.City + "," + res.Country
		if _, exists := cityMap[cityKey]; !exists {
			cityMap[cityKey] = map[string]interface{}{
				"city":            res.City,
				"country":         res.Country,
				"country_code":    "",
				"sessions":        0,
				"users":           0,
				"page_views":      0,
				"bounce_rate":     "0.00%",
				"conversion_rate": "0.00%",
				"revenue":         0.0,
			}
		}
		cityEntry := cityMap[cityKey]
		cityEntry["sessions"] = cityEntry["sessions"].(int) + res.Sessions
		cityEntry["users"] = cityEntry["users"].(int) + res.Users
		cityEntry["page_views"] = cityEntry["page_views"].(int) + res.PageViews
	}

	// Convert to slices
	byCountry := make([]map[string]interface{}, 0, len(countryMap))
	for _, v := range countryMap {
		byCountry = append(byCountry, v)
	}

	byCity := make([]map[string]interface{}, 0, len(cityMap))
	for _, v := range cityMap {
		byCity = append(byCity, v)
	}

	// Sort by sessions
	sortBySessions := func(data []map[string]interface{}) {
		sort.Slice(data, func(i, j int) bool {
			return data[i]["sessions"].(int) > data[j]["sessions"].(int)
		})
	}
	sortBySessions(byCountry)
	sortBySessions(byCity)

	// Summary
	totalCountries := len(countryMap)
	totalCities := len(cityMap)
	topCountry := ""
	topCity := ""
	if len(byCountry) > 0 {
		topCountry = byCountry[0]["country"].(string)
	}
	if len(byCity) > 0 {
		topCity = byCity[0]["city"].(string)
	}

	return map[string]interface{}{
		"by_country": byCountry,
		"by_city":    byCity,
		"summary": map[string]interface{}{
			"total_countries": totalCountries,
			"total_cities":    totalCities,
			"top_country":     topCountry,
			"top_city":        topCity,
		},
	}, nil
}

// calculateDeviceAnalytics calculates device and platform analytics
func (eas *EnhancedAnalyticsService) calculateDeviceAnalytics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Use on-the-fly aggregation from events table
	type DeviceResult struct {
		Device    string
		OS        string
		Browser   string
		Sessions  int
		Users     int
		PageViews int
	}

	var results []DeviceResult

	// Adjust end date to include the full day
	endOfDay := end.Add(24*time.Hour - time.Nanosecond)

	err := eas.db.Model(&Event{}).
		Select("device, os, browser, COUNT(DISTINCT session_id) as sessions, COUNT(DISTINCT user_id) as users, SUM(CASE WHEN event_type IN ('page_view', 'pageview') THEN 1 ELSE 0 END) as page_views").
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, start, endOfDay).
		Group("device, os, browser").
		Scan(&results).Error

	if err != nil {
		log.Printf("Error calculating device analytics for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to calculate device analytics: %v", err)
	}

	log.Printf("Calculated device analytics from %d records for project %s", len(results), projectID)

	// Aggregate maps
	deviceMap := make(map[string]map[string]interface{})
	osMap := make(map[string]map[string]interface{})
	browserMap := make(map[string]map[string]interface{})

	for _, res := range results {
		if res.Device == "" {
			res.Device = "Unknown"
		}
		if res.OS == "" {
			res.OS = "Unknown"
		}
		if res.Browser == "" {
			res.Browser = "Unknown"
		}

		// Device aggregation
		if _, exists := deviceMap[res.Device]; !exists {
			deviceMap[res.Device] = map[string]interface{}{
				"device":           res.Device,
				"sessions":         0,
				"users":            0,
				"page_views":       0,
				"bounce_rate":      "0.00%",
				"avg_session_time": "0s",
				"conversion_rate":  "0.00%",
			}
		}
		dEntry := deviceMap[res.Device]
		dEntry["sessions"] = dEntry["sessions"].(int) + res.Sessions
		dEntry["users"] = dEntry["users"].(int) + res.Users
		dEntry["page_views"] = dEntry["page_views"].(int) + res.PageViews

		// OS aggregation
		if _, exists := osMap[res.OS]; !exists {
			osMap[res.OS] = map[string]interface{}{
				"os":              res.OS,
				"sessions":        0,
				"users":           0,
				"conversion_rate": "0.00%",
			}
		}
		oEntry := osMap[res.OS]
		oEntry["sessions"] = oEntry["sessions"].(int) + res.Sessions
		oEntry["users"] = oEntry["users"].(int) + res.Users

		// Browser aggregation
		if _, exists := browserMap[res.Browser]; !exists {
			browserMap[res.Browser] = map[string]interface{}{
				"browser":  res.Browser,
				"sessions": 0,
				"users":    0,
			}
		}
		bEntry := browserMap[res.Browser]
		bEntry["sessions"] = bEntry["sessions"].(int) + res.Sessions
		bEntry["users"] = bEntry["users"].(int) + res.Users
	}

	// Convert to slices
	byDevice := make([]map[string]interface{}, 0, len(deviceMap))
	for _, v := range deviceMap {
		byDevice = append(byDevice, v)
	}

	byOS := make([]map[string]interface{}, 0, len(osMap))
	for _, v := range osMap {
		byOS = append(byOS, v)
	}

	byBrowser := make([]map[string]interface{}, 0, len(browserMap))
	for _, v := range browserMap {
		byBrowser = append(byBrowser, v)
	}

	// Sort by sessions
	sortBySessions := func(data []map[string]interface{}) {
		sort.Slice(data, func(i, j int) bool {
			return data[i]["sessions"].(int) > data[j]["sessions"].(int)
		})
	}
	sortBySessions(byDevice)
	sortBySessions(byOS)
	sortBySessions(byBrowser)

	return map[string]interface{}{
		"by_device":  byDevice,
		"by_os":      byOS,
		"by_browser": byBrowser,
	}, nil
}

// calculateRetentionCohorts calculates user retention by signup cohorts (Daily)
func (eas *EnhancedAnalyticsService) calculateRetentionCohorts(projectID string, start, end time.Time) ([]map[string]interface{}, error) {
	// Struct to hold query results
	type RetentionResult struct {
		Cohort      string
		TotalUsers  int
		DayNumber   int
		ActiveUsers int
	}

	var results []RetentionResult

	// SQL query to calculate daily retention
	query := `
		WITH user_cohorts AS (
			SELECT user_id, date_trunc('day', MIN(timestamp)) as cohort_date
			FROM events
			WHERE project_id = ?
			GROUP BY user_id
		),
		cohort_sizes AS (
			SELECT cohort_date, COUNT(DISTINCT user_id) as total_users
			FROM user_cohorts
			GROUP BY cohort_date
		),
		user_activity AS (
			SELECT DISTINCT user_id, date_trunc('day', timestamp) as activity_date
			FROM events
			WHERE project_id = ?
			AND timestamp >= ?
		)
		SELECT
			to_char(uc.cohort_date, 'YYYY-MM-DD') as cohort,
			cs.total_users,
			CAST(EXTRACT(DAY FROM (ua.activity_date - uc.cohort_date)) AS INTEGER) as day_number,
			COUNT(DISTINCT uc.user_id) as active_users
		FROM user_cohorts uc
		JOIN cohort_sizes cs ON uc.cohort_date = cs.cohort_date
		JOIN user_activity ua ON uc.user_id = ua.user_id
		WHERE uc.cohort_date BETWEEN ? AND ?
		GROUP BY 1, 2, 3
		ORDER BY 1, 3
	`

	// Execute query
	// We need start date for user_activity filter to optimize, let's use start date of cohorts
	err := eas.db.Raw(query, projectID, projectID, start, start, end).Scan(&results).Error
	if err != nil {
		log.Printf("Error calculating retention cohorts for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to calculate retention cohorts: %v", err)
	}

	log.Printf("Calculated daily retention from %d records for project %s", len(results), projectID)

	// Group by cohort date
	cohortMap := make(map[string]map[string]interface{})

	for _, res := range results {
		if _, exists := cohortMap[res.Cohort]; !exists {
			cohortMap[res.Cohort] = map[string]interface{}{
				"cohort_date": res.Cohort,
				"users":       res.TotalUsers,
				"retention":   make(map[string]float64),
			}
		}

		if res.TotalUsers > 0 {
			// Calculate retention rate
			rate := (float64(res.ActiveUsers) / float64(res.TotalUsers)) * 100

			// Store in map
			retention := cohortMap[res.Cohort]["retention"].(map[string]float64)
			retention[fmt.Sprintf("day_%d", res.DayNumber)] = rate
		}
	}

	// Convert to slice with additional metrics
	cohorts := make([]map[string]interface{}, 0, len(cohortMap))
	for _, cohort := range cohortMap {
		// Calculate cohort health metrics (Day 1 retention is key)
		retention := cohort["retention"].(map[string]float64)

		// Add calculated metrics
		if val, ok := retention["day_1"]; ok {
			cohort["day_1_retention"] = val
		} else {
			cohort["day_1_retention"] = 0.0
		}

		// Calculate average retention
		avgRetention := 0.0
		retentionCount := 0
		for _, rate := range retention {
			avgRetention += rate
			retentionCount++
		}
		if retentionCount > 0 {
			avgRetention = avgRetention / float64(retentionCount)
		}
		cohort["avg_retention"] = fmt.Sprintf("%.2f%%", avgRetention)

		cohorts = append(cohorts, cohort)
	}

	// Sort by cohort date (newest first)
	sort.Slice(cohorts, func(i, j int) bool {
		date1, _ := cohorts[i]["cohort_date"].(string)
		date2, _ := cohorts[j]["cohort_date"].(string)
		return date1 > date2
	})

	return cohorts, nil
}

// calculateFeatureAdoption calculates feature adoption metrics
func (eas *EnhancedAnalyticsService) calculateFeatureAdoption(projectID string, start, end time.Time) ([]map[string]interface{}, error) {
	// Query FeatureAdoption from database with proper error handling
	var featureData []FeatureAdoption
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("adoption_rate DESC").
		Find(&featureData).Error

	if err != nil {
		log.Printf("Error fetching feature adoption for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to fetch feature adoption: %v", err)
	}

	log.Printf("Found %d feature adoption records for project %s", len(featureData), projectID)

	// Return empty array if no data found
	if len(featureData) == 0 {
		return []map[string]interface{}{}, nil
	}

	// Group by feature name and aggregate with improved logic
	featureMap := make(map[string]*FeatureAdoption)
	featureDataPoints := make(map[string]int) // Track number of data points for weighted averages

	for _, data := range featureData {
		// Validate feature data
		if data.FeatureName == "" {
			log.Printf("Warning: Empty feature name for project %s", projectID)
			continue
		}

		if data.TotalUsers < 0 || data.UsersWhoTriedFeature < 0 {
			log.Printf("Warning: Invalid user counts for feature %s in project %s", data.FeatureName, projectID)
			continue
		}

		if existing, ok := featureMap[data.FeatureName]; ok {
			// Calculate weighted averages for rates based on user counts
			totalUsersSum := existing.TotalUsers + data.TotalUsers
			dataPoints := featureDataPoints[data.FeatureName] + 1

			// Aggregate user counts
			existing.TotalUsers += data.TotalUsers
			existing.UsersWhoTriedFeature += data.UsersWhoTriedFeature
			existing.DailyActiveFeature += data.DailyActiveFeature
			existing.WeeklyActiveFeature += data.WeeklyActiveFeature
			existing.MonthlyActiveFeature += data.MonthlyActiveFeature

			// Calculate weighted averages for rates
			if totalUsersSum > 0 {
				existing.AdoptionRate = ((existing.AdoptionRate * float64(existing.TotalUsers)) + (data.AdoptionRate * float64(data.TotalUsers))) / float64(totalUsersSum)
				existing.FeatureStickiness = ((existing.FeatureStickiness * float64(existing.TotalUsers)) + (data.FeatureStickiness * float64(data.TotalUsers))) / float64(totalUsersSum)
			} else {
				existing.AdoptionRate = (existing.AdoptionRate + data.AdoptionRate) / float64(dataPoints)
				existing.FeatureStickiness = (existing.FeatureStickiness + data.FeatureStickiness) / float64(dataPoints)
			}

			existing.TimeToFirstUse = int((float64(existing.TimeToFirstUse) + float64(data.TimeToFirstUse)) / float64(dataPoints))
			existing.DropoffAfterFirstUse = (existing.DropoffAfterFirstUse + data.DropoffAfterFirstUse) / float64(dataPoints)

			featureDataPoints[data.FeatureName] = dataPoints
		} else {
			dataCopy := data
			featureMap[data.FeatureName] = &dataCopy
			featureDataPoints[data.FeatureName] = 1
		}
	}

	// Convert to slice with proper formatting and validation
	features := make([]map[string]interface{}, 0, len(featureMap))
	for _, data := range featureMap {
		// Validate data before adding
		if data.TotalUsers < 0 || data.UsersWhoTriedFeature < 0 {
			log.Printf("Warning: Invalid feature adoption data for feature %s", data.FeatureName)
			continue
		}

		// Format time to first use
		timeToFirstUseStr := fmt.Sprintf("%d days", data.TimeToFirstUse)
		if data.TimeToFirstUse == 0 {
			timeToFirstUseStr = "Same day"
		} else if data.TimeToFirstUse == 1 {
			timeToFirstUseStr = "1 day"
		}

		features = append(features, map[string]interface{}{
			"feature_name":        data.FeatureName,
			"total_users":         data.TotalUsers,
			"adopted_users":       data.UsersWhoTriedFeature,
			"adoption_rate":       fmt.Sprintf("%.2f%%", data.AdoptionRate),
			"daily_active":        data.DailyActiveFeature,
			"weekly_active":       data.WeeklyActiveFeature,
			"monthly_active":      data.MonthlyActiveFeature,
			"stickiness":          fmt.Sprintf("%.2f%%", data.FeatureStickiness*100), // Convert to percentage
			"time_to_first_use":   timeToFirstUseStr,
			"dropoff_after_first": fmt.Sprintf("%.2f%%", data.DropoffAfterFirstUse),
		})
	}

	// Improved sorting by adoption rate with error handling
	for i := 0; i < len(features)-1; i++ {
		for j := i + 1; j < len(features); j++ {
			// Extract numeric values for comparison
			rate1Str, ok1 := features[i]["adoption_rate"].(string)
			rate2Str, ok2 := features[j]["adoption_rate"].(string)

			if ok1 && ok2 {
				// Remove % and convert to float for comparison
				rate1, err1 := strconv.ParseFloat(strings.TrimSuffix(rate1Str, "%"), 64)
				rate2, err2 := strconv.ParseFloat(strings.TrimSuffix(rate2Str, "%"), 64)

				if err1 == nil && err2 == nil && rate1 < rate2 {
					features[i], features[j] = features[j], features[i]
				}
			}
		}
	}

	log.Printf("Returning %d features for project %s", len(features), projectID)
	return features, nil
}

// calculateChurnRisk calculates churn risk scores and at-risk users based on user behavior
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (eas *EnhancedAnalyticsService) calculateChurnRisk(projectID string, threshold float64) (map[string]interface{}, error) {
	// Validate threshold parameter
	if threshold < 0 || threshold > 100 {
		threshold = 70 // Default to 70%
		log.Printf("Warning: Invalid threshold %.2f%%, defaulting to 70%% for project %s", threshold, projectID)
	}

	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -90)
	thirtyDaysAgo := endDate.AddDate(0, 0, -30)

	// OPTIMIZED: Use SQL aggregation to get user-level stats instead of loading all events
	type UserStatDB struct {
		UserID        string    `gorm:"column:user_id"`
		FirstActive   time.Time `gorm:"column:first_active"`
		LastActive    time.Time `gorm:"column:last_active"`
		SessionCount  int64     `gorm:"column:session_count"`
		ActiveDays    int64     `gorm:"column:active_days"`
		EventsLast30d int64     `gorm:"column:events_last_30d"`
		EventsPrev60d int64     `gorm:"column:events_prev_60d"`
	}

	var userStats []UserStatDB

	// Single aggregated query to get all user stats
	err := eas.db.Raw(`
		SELECT 
			user_id,
			MIN(timestamp) as first_active,
			MAX(timestamp) as last_active,
			COUNT(DISTINCT session_id) as session_count,
			COUNT(DISTINCT date_trunc('day', timestamp)) as active_days,
			SUM(CASE WHEN timestamp > ? THEN 1 ELSE 0 END) as events_last_30d,
			SUM(CASE WHEN timestamp <= ? THEN 1 ELSE 0 END) as events_prev_60d
		FROM events
		WHERE project_id = ? 
			AND timestamp BETWEEN ? AND ?
			AND user_id IS NOT NULL 
			AND user_id != ''
		GROUP BY user_id
	`, thirtyDaysAgo, thirtyDaysAgo, projectID, startDate, endDate).Scan(&userStats).Error

	if err != nil {
		log.Printf("Error fetching user stats for churn analysis: %v", err)
		return nil, fmt.Errorf("failed to fetch user stats: %v", err)
	}

	if len(userStats) == 0 {
		return map[string]interface{}{
			"at_risk_users": []map[string]interface{}{},
			"churn_stats": map[string]interface{}{
				"total_users":    0,
				"at_risk_users":  0,
				"churn_rate_30d": "0.00%",
			},
		}, nil
	}

	log.Printf("Calculating churn risk for %d users in project %s", len(userStats), projectID)

	// Calculate Risk Scores
	type UserRisk struct {
		data      map[string]interface{}
		riskScore float64
	}
	var atRiskUsers []UserRisk
	riskBuckets := map[string]int{
		"Critical": 0,
		"High":     0,
		"Medium":   0,
		"Low":      0,
	}

	totalUsers := len(userStats)
	churnedCount := 0

	for _, stat := range userStats {
		// Calculate Recency (Days since last active)
		daysSinceActive := endDate.Sub(stat.LastActive).Hours() / 24.0

		// Calculate Frequency (Sessions per week)
		activeWeeks := endDate.Sub(stat.FirstActive).Hours() / (24 * 7)
		if activeWeeks < 1 {
			activeWeeks = 1
		}
		frequency := float64(stat.SessionCount) / activeWeeks

		// Calculate Trend (Activity change)
		trendScore := 0.0
		if stat.EventsPrev60d > 0 {
			// Compare last 30 days to average of previous 60 days
			prevAvg := float64(stat.EventsPrev60d) / 2.0
			if prevAvg > 0 {
				change := (float64(stat.EventsLast30d) - prevAvg) / prevAvg
				if change < -0.5 {
					trendScore = -20 // Significant drop
				} else if change < -0.2 {
					trendScore = -10 // Moderate drop
				} else if change > 0.2 {
					trendScore = 10 // Improvement
				}
			}
		}

		// --- Health Score Calculation (0-100) ---
		// 1. Recency Score (40%): High if recently active
		recencyScore := 100.0 - (daysSinceActive * 2)
		if recencyScore < 0 {
			recencyScore = 0
		}

		// 2. Frequency Score (30%): High if frequent (3 sessions/week is "good")
		freqScore := (frequency / 3.0) * 100.0
		if freqScore > 100 {
			freqScore = 100
		}

		// 3. Engagement/Trend Score (30%)
		daysActiveRatio := float64(stat.ActiveDays) / 90.0
		engagementScore := (daysActiveRatio * 100.0) + trendScore
		if engagementScore > 100 {
			engagementScore = 100
		} else if engagementScore < 0 {
			engagementScore = 0
		}

		// Weighted Health Score
		healthScore := (recencyScore * 0.4) + (freqScore * 0.3) + (engagementScore * 0.3)

		// Churn Risk is inverse of Health Score
		churnRisk := 100.0 - healthScore

		// Determine Risk Category
		category := "Low"
		if churnRisk >= 80 {
			category = "Critical"
			riskBuckets["Critical"]++
		} else if churnRisk >= 60 {
			category = "High"
			riskBuckets["High"]++
		} else if churnRisk >= 40 {
			category = "Medium"
			riskBuckets["Medium"]++
		} else {
			riskBuckets["Low"]++
		}

		// Identify Churned Users (Inactive > 30 days)
		isChurned := daysSinceActive > 30
		if isChurned {
			churnedCount++
		}

		// Add to list if above threshold or churned
		if churnRisk >= threshold || isChurned {
			atRiskUsers = append(atRiskUsers, UserRisk{
				riskScore: churnRisk,
				data: map[string]interface{}{
					"user_id":           stat.UserID,
					"risk_score":        fmt.Sprintf("%.1f%%", churnRisk),
					"health_score":      fmt.Sprintf("%.1f", healthScore),
					"category":          category,
					"last_active":       stat.LastActive.Format("2006-01-02"),
					"days_inactive":     int(daysSinceActive),
					"sessions_total":    stat.SessionCount,
					"avg_sessions_week": fmt.Sprintf("%.1f", frequency),
					"is_churned":        isChurned,
					"trend":             trendScore,
				},
			})
		}
	}

	// Sort by risk score descending (using actual float values now)
	sort.Slice(atRiskUsers, func(i, j int) bool {
		return atRiskUsers[i].riskScore > atRiskUsers[j].riskScore
	})

	// Limit to top 50 and extract data maps
	resultUsers := make([]map[string]interface{}, 0, 50)
	for i, u := range atRiskUsers {
		if i >= 50 {
			break
		}
		resultUsers = append(resultUsers, u.data)
	}

	// Calculate Churn Rate
	churnRate := 0.0
	if totalUsers > 0 {
		churnRate = (float64(churnedCount) / float64(totalUsers)) * 100
	}

	result := map[string]interface{}{
		"at_risk_users": resultUsers,
		"churn_stats": map[string]interface{}{
			"total_users":    totalUsers,
			"at_risk_users":  len(resultUsers),
			"churned_users":  churnedCount,
			"churn_rate_30d": fmt.Sprintf("%.2f%%", churnRate),
			"risk_breakdown": riskBuckets,
		},
	}

	return result, nil
}

// calculateConversionFunnel calculates conversion funnel metrics
func (eas *EnhancedAnalyticsService) calculateConversionFunnel(projectID string, funnelName string, start, end time.Time) ([]map[string]interface{}, error) {
	// Validate funnel name
	if funnelName == "" {
		funnelName = "default"
		log.Printf("Warning: No funnel name provided, using 'default' for project %s", projectID)
	}

	// Query ConversionFunnel from database with proper error handling
	var funnelData []ConversionFunnel
	query := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end)

	if funnelName != "default" {
		query = query.Where("funnel_name = ?", funnelName)
	}

	err := query.Order("step_number ASC").Find(&funnelData).Error
	if err != nil {
		log.Printf("Error fetching conversion funnel for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to fetch conversion funnel: %v", err)
	}

	log.Printf("Found %d funnel records for project %s, funnel %s", len(funnelData), projectID, funnelName)

	// Return empty array if no data found
	if len(funnelData) == 0 {
		return []map[string]interface{}{}, nil
	}

	// Group by step number and aggregate with improved logic
	stepMap := make(map[int]*ConversionFunnel)
	stepDataPoints := make(map[int]int) // Track number of data points for weighted averages

	for _, data := range funnelData {
		// Validate funnel data
		if data.StepNumber <= 0 {
			log.Printf("Warning: Invalid step number %d for project %s", data.StepNumber, projectID)
			continue
		}

		if data.Users < 0 {
			log.Printf("Warning: Invalid user count %d for step %d in project %s", data.Users, data.StepNumber, projectID)
			continue
		}

		if existing, ok := stepMap[data.StepNumber]; ok {
			// Calculate weighted averages based on user counts
			totalUsers := existing.Users + data.Users
			dataPoints := stepDataPoints[data.StepNumber] + 1

			// Aggregate user counts and revenue
			existing.Users += data.Users
			existing.Revenue += data.Revenue

			// Calculate weighted averages for rates
			if totalUsers > 0 {
				existing.ConversionRate = ((existing.ConversionRate * float64(existing.Users)) + (data.ConversionRate * float64(data.Users))) / float64(totalUsers)
				existing.DropoffRate = ((existing.DropoffRate * float64(existing.Users)) + (data.DropoffRate * float64(data.Users))) / float64(totalUsers)
			} else {
				existing.ConversionRate = (existing.ConversionRate + data.ConversionRate) / float64(dataPoints)
				existing.DropoffRate = (existing.DropoffRate + data.DropoffRate) / float64(dataPoints)
			}

			existing.AvgTimeInStep = int((float64(existing.AvgTimeInStep) + float64(data.AvgTimeInStep)) / float64(dataPoints))
			stepDataPoints[data.StepNumber] = dataPoints
		} else {
			dataCopy := data
			stepMap[data.StepNumber] = &dataCopy
			stepDataPoints[data.StepNumber] = 1
		}
	}

	// Convert to slice with proper formatting and validation
	funnelSteps := make([]map[string]interface{}, 0, len(stepMap))
	for _, data := range stepMap {
		// Validate data before adding
		if data.Users < 0 {
			log.Printf("Warning: Invalid funnel step data for step %d", data.StepNumber)
			continue
		}

		// Format time in step
		timeInStepStr := fmt.Sprintf("%d seconds", data.AvgTimeInStep)
		if data.AvgTimeInStep >= 60 {
			minutes := data.AvgTimeInStep / 60
			seconds := data.AvgTimeInStep % 60
			if minutes >= 60 {
				hours := minutes / 60
				minutes = minutes % 60
				timeInStepStr = fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
			} else {
				timeInStepStr = fmt.Sprintf("%dm %ds", minutes, seconds)
			}
		}

		funnelSteps = append(funnelSteps, map[string]interface{}{
			"step_number":      data.StepNumber,
			"step_name":        data.StepName,
			"users":            data.Users,
			"conversion_rate":  fmt.Sprintf("%.2f%%", data.ConversionRate),
			"dropoff_rate":     fmt.Sprintf("%.2f%%", data.DropoffRate),
			"avg_time_in_step": timeInStepStr,
			"revenue":          fmt.Sprintf("$%.2f", float64(data.Revenue)/100), // Convert cents to dollars
		})
	}

	// Improved sorting by step number with error handling
	for i := 0; i < len(funnelSteps)-1; i++ {
		for j := i + 1; j < len(funnelSteps); j++ {
			step1, ok1 := funnelSteps[i]["step_number"].(int)
			step2, ok2 := funnelSteps[j]["step_number"].(int)
			if ok1 && ok2 && step1 > step2 {
				funnelSteps[i], funnelSteps[j] = funnelSteps[j], funnelSteps[i]
			}
		}
	}

	log.Printf("Returning %d funnel steps for project %s, funnel %s", len(funnelSteps), projectID, funnelName)
	return funnelSteps, nil
}

// calculateSessionMetrics calculates enhanced session analytics from events
// OPTIMIZED: Uses single aggregated SQL query instead of N+1 per-day queries
func (eas *EnhancedAnalyticsService) calculateSessionMetrics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Adjust end date to include the full day
	endOfDay := end.Add(24*time.Hour - time.Nanosecond)

	log.Printf("Calculating session metrics for project %s from %s to %s", projectID, start.Format("2006-01-02"), end.Format("2006-01-02"))

	// OPTIMIZED: Get total sessions and unique users in a single query
	type AggregatedCounts struct {
		TotalSessions int64
		UniqueUsers   int64
	}
	var counts AggregatedCounts
	err := eas.db.Model(&Event{}).
		Select("COUNT(DISTINCT session_id) as total_sessions, COUNT(DISTINCT user_id) as unique_users").
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, start, endOfDay).
		Scan(&counts).Error

	if err != nil {
		log.Printf("Error counting sessions/users for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to count sessions/users: %v", err)
	}

	totalSessions := counts.TotalSessions
	uniqueUsers := counts.UniqueUsers

	log.Printf("Found %d sessions and %d unique users for project %s", totalSessions, uniqueUsers, projectID)

	// Return empty structure if no data found
	if totalSessions == 0 {
		return map[string]interface{}{
			"overview": map[string]interface{}{
				"total_sessions":       0,
				"unique_users":         0,
				"avg_session_duration": "0 seconds",
				"bounce_rate":          "0.00%",
				"return_visitor_rate":  "0.00%",
			},
			"engagement": map[string]interface{}{
				"dau":               0,
				"wau":               0,
				"mau":               0,
				"stickiness_ratio":  "0.00%",
				"session_frequency": "0.00",
			},
			"time_series": []map[string]interface{}{},
		}, nil
	}

	// Calculate session durations by grouping events per session
	type SessionInfo struct {
		SessionID string
		UserID    string
		MinTime   time.Time
		MaxTime   time.Time
		Events    int
	}

	var sessions []SessionInfo
	err = eas.db.Model(&Event{}).
		Select("session_id, user_id, MIN(timestamp) as min_time, MAX(timestamp) as max_time, COUNT(*) as events").
		Where("project_id = ? AND timestamp BETWEEN ? AND ? AND session_id != ''", projectID, start, endOfDay).
		Group("session_id, user_id").
		Scan(&sessions).Error

	if err != nil {
		log.Printf("Error calculating session durations for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to calculate session durations: %v", err)
	}

	// Calculate metrics from sessions
	totalDuration := 0
	singleEventSessions := 0
	returningUsers := make(map[string]int) // user_id -> session count

	for _, session := range sessions {
		duration := int(session.MaxTime.Sub(session.MinTime).Seconds())
		totalDuration += duration

		if session.Events == 1 {
			singleEventSessions++
		}

		returningUsers[session.UserID]++
	}

	avgDuration := 0
	if len(sessions) > 0 {
		avgDuration = totalDuration / len(sessions)
	}

	bounceRate := 0.0
	if len(sessions) > 0 {
		bounceRate = (float64(singleEventSessions) / float64(len(sessions))) * 100
	}

	// Calculate return visitor rate
	returnCount := 0
	for _, count := range returningUsers {
		if count > 1 {
			returnCount++
		}
	}
	returnRate := 0.0
	if len(returningUsers) > 0 {
		returnRate = (float64(returnCount) / float64(len(returningUsers))) * 100
	}

	// OPTIMIZED: Get DAU, WAU, MAU in a single query using date ranges
	dayStart := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	dayEnd := dayStart.Add(24*time.Hour - time.Nanosecond)
	weekStart := end.AddDate(0, 0, -6)   // 7 days including today
	monthStart := end.AddDate(0, 0, -29) // 30 days including today

	type EngagementCounts struct {
		DAU int64
		WAU int64
		MAU int64
	}
	var engagement EngagementCounts

	// DAU query
	eas.db.Model(&Event{}).
		Select("COUNT(DISTINCT user_id) as dau").
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, dayStart, dayEnd).
		Scan(&engagement)

	// WAU and MAU in parallel (could use goroutines, but these are fast)
	var wauCount, mauCount int64
	eas.db.Model(&Event{}).
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, weekStart, endOfDay).
		Distinct("user_id").
		Count(&wauCount)
	engagement.WAU = wauCount

	eas.db.Model(&Event{}).
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, monthStart, endOfDay).
		Distinct("user_id").
		Count(&mauCount)
	engagement.MAU = mauCount

	// Calculate stickiness ratio (DAU/MAU)
	stickinessRatio := 0.0
	if engagement.MAU > 0 {
		stickinessRatio = (float64(engagement.DAU) / float64(engagement.MAU))
	}

	// Calculate session frequency
	sessionFreq := 0.0
	if uniqueUsers > 0 {
		sessionFreq = float64(totalSessions) / float64(uniqueUsers)
	}

	// OPTIMIZED: Build time series using a SINGLE aggregated query with GROUP BY
	// This replaces the N+1 query loop that was causing 54 queries (2 per day × 27 days)
	type DailyStats struct {
		Date     time.Time `gorm:"column:day_date"`
		Sessions int64     `gorm:"column:sessions"`
		Users    int64     `gorm:"column:users"`
	}

	var dailyStats []DailyStats
	err = eas.db.Model(&Event{}).
		Select(`
			date_trunc('day', timestamp) as day_date,
			COUNT(DISTINCT session_id) as sessions,
			COUNT(DISTINCT user_id) as users
		`).
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, start, endOfDay).
		Group("date_trunc('day', timestamp)").
		Order("day_date ASC").
		Scan(&dailyStats).Error

	if err != nil {
		log.Printf("Error fetching daily stats for project %s: %v", projectID, err)
		// Non-fatal, return empty time series
		dailyStats = []DailyStats{}
	}

	// Convert to time series format
	timeSeries := make([]map[string]interface{}, 0, len(dailyStats))
	for _, day := range dailyStats {
		if day.Sessions > 0 || day.Users > 0 {
			timeSeries = append(timeSeries, map[string]interface{}{
				"date":     day.Date.Format("2006-01-02"),
				"sessions": day.Sessions,
				"users":    day.Users,
			})
		}
	}

	// Format average duration for display
	avgDurationStr := fmt.Sprintf("%d seconds", avgDuration)
	if avgDuration >= 60 {
		minutes := avgDuration / 60
		seconds := avgDuration % 60
		if minutes >= 60 {
			hours := minutes / 60
			minutes = minutes % 60
			avgDurationStr = fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
		} else {
			avgDurationStr = fmt.Sprintf("%dm %ds", minutes, seconds)
		}
	}

	sessionData := map[string]interface{}{
		"overview": map[string]interface{}{
			"total_sessions":       totalSessions,
			"unique_users":         uniqueUsers,
			"avg_session_duration": avgDurationStr,
			"bounce_rate":          fmt.Sprintf("%.2f%%", bounceRate),
			"return_visitor_rate":  fmt.Sprintf("%.2f%%", returnRate),
		},
		"engagement": map[string]interface{}{
			"dau":               engagement.DAU,
			"wau":               engagement.WAU,
			"mau":               engagement.MAU,
			"stickiness_ratio":  fmt.Sprintf("%.2f%%", stickinessRatio*100),
			"session_frequency": fmt.Sprintf("%.2f", sessionFreq),
		},
		"time_series": timeSeries,
		"meta": map[string]interface{}{
			"date_range":   fmt.Sprintf("%s to %s", start.Format("2006-01-02"), end.Format("2006-01-02")),
			"data_points":  len(timeSeries),
			"total_events": len(sessions),
		},
	}

	log.Printf("Returning session metrics for project %s: %d sessions, %d unique users, DAU: %d, WAU: %d, MAU: %d",
		projectID, totalSessions, uniqueUsers, engagement.DAU, engagement.WAU, engagement.MAU)
	return sessionData, nil
}
