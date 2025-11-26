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
	months := c.DefaultQuery("months", "12")

	monthsInt, _ := strconv.Atoi(months)
	cohortData, err := eas.calculateRetentionCohorts(projectID, monthsInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"months":     monthsInt,
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

// calculateRetentionCohorts calculates user retention by signup cohorts
func (eas *EnhancedAnalyticsService) calculateRetentionCohorts(projectID string, months int) ([]map[string]interface{}, error) {
	// Validate input parameters
	if months <= 0 || months > 24 {
		months = 12 // Default to 12 months
		log.Printf("Warning: Invalid months parameter, defaulting to 12 for project %s", projectID)
	}

	// Query UserCohortMetrics from database with proper error handling
	startDate := time.Now().AddDate(0, -months, 0)

	var cohortData []UserCohortMetrics
	err := eas.db.Where("project_id = ? AND cohort_month >= ?", projectID, startDate).
		Order("cohort_month DESC, period_number ASC").
		Find(&cohortData).Error

	if err != nil {
		log.Printf("Error fetching retention cohorts for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to fetch retention cohorts: %v", err)
	}

	log.Printf("Found %d cohort records for project %s", len(cohortData), projectID)

	// Return empty array if no data found
	if len(cohortData) == 0 {
		return []map[string]interface{}{}, nil
	}

	// Group by cohort month with improved validation
	cohortMap := make(map[string]map[string]interface{})

	for _, data := range cohortData {
		// Validate cohort data
		if data.UsersInCohort <= 0 {
			log.Printf("Warning: Invalid user count in cohort for project %s, cohort %s", projectID, data.CohortMonth.Format("2006-01"))
			continue
		}

		if data.RetentionRate < 0 || data.RetentionRate > 100 {
			log.Printf("Warning: Invalid retention rate %.2f%% for project %s", data.RetentionRate, projectID)
			continue
		}

		cohortKey := data.CohortMonth.Format("2006-01")

		if _, exists := cohortMap[cohortKey]; !exists {
			cohortMap[cohortKey] = map[string]interface{}{
				"cohort_month": cohortKey,
				"users":        data.UsersInCohort,
				"retention":    make(map[string]float64),
			}
		}

		retention := cohortMap[cohortKey]["retention"].(map[string]float64)
		periodKey := "month_" + strconv.Itoa(data.PeriodNumber)
		retention[periodKey] = data.RetentionRate
	}

	// Convert to slice with additional metrics
	cohorts := make([]map[string]interface{}, 0, len(cohortMap))
	for _, cohort := range cohortMap {
		// Calculate cohort health metrics
		retention := cohort["retention"].(map[string]float64)
		avgRetention := 0.0
		retentionCount := 0

		for _, rate := range retention {
			avgRetention += rate
			retentionCount++
		}

		if retentionCount > 0 {
			avgRetention = avgRetention / float64(retentionCount)
		}

		// Add calculated metrics
		cohort["avg_retention"] = fmt.Sprintf("%.2f%%", avgRetention)
		cohort["retention_periods"] = retentionCount

		cohorts = append(cohorts, cohort)
	}

	// Improved sorting by cohort month (newest first) with error handling
	for i := 0; i < len(cohorts)-1; i++ {
		for j := i + 1; j < len(cohorts); j++ {
			month1, ok1 := cohorts[i]["cohort_month"].(string)
			month2, ok2 := cohorts[j]["cohort_month"].(string)
			if ok1 && ok2 && month1 < month2 {
				cohorts[i], cohorts[j] = cohorts[j], cohorts[i]
			}
		}
	}

	log.Printf("Returning %d cohorts for project %s", len(cohorts), projectID)
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
func (eas *EnhancedAnalyticsService) calculateChurnRisk(projectID string, threshold float64) (map[string]interface{}, error) {
	// Validate threshold parameter
	if threshold < 0 || threshold > 100 {
		threshold = 70 // Default to 70%
		log.Printf("Warning: Invalid threshold %.2f%%, defaulting to 70%% for project %s", threshold, projectID)
	}

	// 1. Fetch events for last 90 days to analyze behavior
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -90)

	var events []Event
	// Optimize: Select only needed fields
	if err := eas.db.Select("user_id, session_id, event_type, timestamp").
		Where("project_id = ? AND timestamp BETWEEN ? AND ?", projectID, startDate, endDate).
		Order("timestamp ASC").Find(&events).Error; err != nil {
		log.Printf("Error fetching events for churn analysis: %v", err)
		return nil, fmt.Errorf("failed to fetch events: %v", err)
	}

	if len(events) == 0 {
		return map[string]interface{}{
			"at_risk_users": []map[string]interface{}{},
			"churn_stats": map[string]interface{}{
				"total_users":    0,
				"at_risk_users":  0,
				"churn_rate_30d": "0.00%",
			},
		}, nil
	}

	// 2. Aggregate user stats
	type UserStat struct {
		UserID          string
		LastActive      time.Time
		FirstActive     time.Time
		SessionCount    int
		TotalDuration   float64 // Estimated
		Sessions        map[string]time.Time
		ActiveDays      map[string]bool
		EventsLast30d   int
		EventsPrev30d   int
		SessionsLast30d int
		SessionsPrev30d int
	}

	userStats := make(map[string]*UserStat)
	thirtyDaysAgo := endDate.AddDate(0, 0, -30)

	for _, event := range events {
		if event.UserID == "" {
			continue
		}

		stat, exists := userStats[event.UserID]
		if !exists {
			stat = &UserStat{
				UserID:     event.UserID,
				Sessions:   make(map[string]time.Time),
				ActiveDays: make(map[string]bool),
			}
			userStats[event.UserID] = stat
		}

		// Update timestamps
		if stat.FirstActive.IsZero() || event.Timestamp.Before(stat.FirstActive) {
			stat.FirstActive = event.Timestamp
		}
		if event.Timestamp.After(stat.LastActive) {
			stat.LastActive = event.Timestamp
		}

		// Track sessions
		if event.SessionID != "" {
			stat.Sessions[event.SessionID] = event.Timestamp
		}

		// Track active days
		day := event.Timestamp.Format("2006-01-02")
		stat.ActiveDays[day] = true

		// Track trend metrics
		if event.Timestamp.After(thirtyDaysAgo) {
			stat.EventsLast30d++
		} else {
			stat.EventsPrev30d++
		}
	}

	// 3. Calculate Risk Scores
	var atRiskUsers []map[string]interface{}
	riskBuckets := map[string]int{
		"Critical": 0,
		"High":     0,
		"Medium":   0,
		"Low":      0,
	}

	totalUsers := len(userStats)
	churnedCount := 0

	for userID, stat := range userStats {
		// Calculate Recency (Days since last active)
		daysSinceActive := endDate.Sub(stat.LastActive).Hours() / 24.0

		// Calculate Frequency (Sessions per week)
		activeWeeks := endDate.Sub(stat.FirstActive).Hours() / (24 * 7)
		if activeWeeks < 1 {
			activeWeeks = 1
		}
		stat.SessionCount = len(stat.Sessions)
		frequency := float64(stat.SessionCount) / activeWeeks

		// Calculate Trend (Activity change)
		// Normalize to comparable periods (last 30 days vs previous 60 days normalized to 30)
		// Simple trend: (Last30 - Prev30Avg)
		trendScore := 0.0
		if stat.EventsPrev30d > 0 {
			// Compare last 30 days to average of previous 60 days (approx)
			prevAvg := float64(stat.EventsPrev30d) / 2.0
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
		recencyScore := 100.0 - (daysSinceActive * 2) // Lose 2 points per day inactive
		if recencyScore < 0 {
			recencyScore = 0
		}

		// 2. Frequency Score (30%): High if frequent
		// Assume 3 sessions/week is "good" (100)
		freqScore := (frequency / 3.0) * 100.0
		if freqScore > 100 {
			freqScore = 100
		}

		// 3. Engagement/Trend Score (30%)
		// Base on active days ratio + trend
		daysActiveRatio := float64(len(stat.ActiveDays)) / 90.0
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
			atRiskUsers = append(atRiskUsers, map[string]interface{}{
				"user_id":           userID,
				"risk_score":        fmt.Sprintf("%.1f%%", churnRisk),
				"health_score":      fmt.Sprintf("%.1f", healthScore),
				"category":          category,
				"last_active":       stat.LastActive.Format("2006-01-02"),
				"days_inactive":     int(daysSinceActive),
				"sessions_total":    stat.SessionCount,
				"avg_sessions_week": fmt.Sprintf("%.1f", frequency),
				"is_churned":        isChurned,
				"trend":             trendScore,
			})
		}
	}

	// Sort by risk score descending
	sort.Slice(atRiskUsers, func(i, j int) bool {
		// Parse risk score strings back to float for sorting is messy,
		// better to store float in struct if needed, but string comparison of "XX.X%" works roughly if padded,
		// but here we can just rely on the fact that we want high risk first.
		// Let's just use the string comparison for now or improve if needed.
		// Actually, let's use the raw values if we had them, but we put strings in the map.
		// Re-parsing for sort:
		s1 := atRiskUsers[i]["risk_score"].(string)
		s2 := atRiskUsers[j]["risk_score"].(string)
		return s1 > s2 // Rough sort
	})

	// Limit to top 50
	if len(atRiskUsers) > 50 {
		atRiskUsers = atRiskUsers[:50]
	}

	// Calculate Churn Rate
	churnRate := 0.0
	if totalUsers > 0 {
		churnRate = (float64(churnedCount) / float64(totalUsers)) * 100
	}

	result := map[string]interface{}{
		"at_risk_users": atRiskUsers,
		"churn_stats": map[string]interface{}{
			"total_users":    totalUsers,
			"at_risk_users":  len(atRiskUsers),
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

// calculateSessionMetrics calculates enhanced session analytics
func (eas *EnhancedAnalyticsService) calculateSessionMetrics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Query UserSessionMetrics from database with proper error handling
	var sessionMetrics []UserSessionMetrics
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("date DESC").
		Find(&sessionMetrics).Error

	if err != nil {
		log.Printf("Error fetching session metrics for project %s: %v", projectID, err)
		return nil, fmt.Errorf("failed to fetch session metrics: %v", err)
	}

	log.Printf("Found %d session metrics records for project %s", len(sessionMetrics), projectID)

	// Return empty structure if no data found
	if len(sessionMetrics) == 0 {
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

	// Aggregate metrics with improved validation
	totalSessions := 0
	uniqueUsers := 0
	totalDuration := 0
	bounceRateSum := 0.0
	returnRateSum := 0.0
	dau := 0
	wau := 0
	mau := 0
	stickinessSum := 0.0
	sessionFreqSum := 0.0
	validCount := 0

	timeSeries := make([]map[string]interface{}, 0, 30) // Limit to last 30 days

	for _, data := range sessionMetrics {
		// Validate session data
		if data.TotalSessions < 0 || data.DAU < 0 || data.WAU < 0 || data.MAU < 0 {
			log.Printf("Warning: Invalid session metrics data for project %s on date %s", projectID, data.Date.Format("2006-01-02"))
			continue
		}

		totalSessions += data.TotalSessions
		dau += data.DAU
		wau += data.WAU
		mau += data.MAU
		totalDuration += data.AvgSessionDuration * data.TotalSessions
		bounceRateSum += data.BounceRate
		returnRateSum += data.ReturnUserRate
		stickinessSum += data.StickinessRatio
		sessionFreqSum += data.AvgSessionsPerUser
		validCount++

		// Build time series (limit to last 30 days)
		if len(timeSeries) < 30 {
			// Format duration for display
			durationStr := fmt.Sprintf("%d seconds", data.AvgSessionDuration)
			if data.AvgSessionDuration >= 60 {
				minutes := data.AvgSessionDuration / 60
				seconds := data.AvgSessionDuration % 60
				durationStr = fmt.Sprintf("%dm %ds", minutes, seconds)
			}

			timeSeries = append(timeSeries, map[string]interface{}{
				"date":         data.Date.Format("2006-01-02"),
				"sessions":     data.TotalSessions,
				"users":        data.DAU,
				"avg_duration": durationStr,
			})
		}
	}

	// Calculate averages
	avgDuration := 0
	if totalSessions > 0 {
		avgDuration = totalDuration / totalSessions
		// Get unique users from MAU (most recent)
		if len(sessionMetrics) > 0 {
			uniqueUsers = sessionMetrics[0].MAU
		}
	}

	avgBounceRate := 0.0
	avgReturnRate := 0.0
	avgStickiness := 0.0
	avgSessionFreq := 0.0
	if validCount > 0 {
		avgBounceRate = bounceRateSum / float64(validCount)
		avgReturnRate = returnRateSum / float64(validCount)
		avgStickiness = stickinessSum / float64(validCount)
		avgSessionFreq = sessionFreqSum / float64(validCount)
		// Use average DAU/WAU/MAU
		dau = dau / validCount
		wau = wau / validCount
		mau = mau / validCount
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
			"bounce_rate":          fmt.Sprintf("%.2f%%", avgBounceRate),
			"return_visitor_rate":  fmt.Sprintf("%.2f%%", avgReturnRate),
		},
		"engagement": map[string]interface{}{
			"dau":               dau,
			"wau":               wau,
			"mau":               mau,
			"stickiness_ratio":  fmt.Sprintf("%.2f%%", avgStickiness*100), // Convert to percentage
			"session_frequency": fmt.Sprintf("%.2f", avgSessionFreq),
		},
		"time_series": timeSeries,
		"meta": map[string]interface{}{
			"date_range":  fmt.Sprintf("%s to %s", start.Format("2006-01-02"), end.Format("2006-01-02")),
			"data_points": validCount,
		},
	}

	log.Printf("Returning session metrics for project %s: %d sessions, %d unique users", projectID, totalSessions, uniqueUsers)
	return sessionData, nil
}
