package main

import (
	"net/http"
	"strconv"
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

// LocationAnalyticsHandler provides geographical analytics
func (eas *EnhancedAnalyticsService) LocationAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	locationData, err := eas.calculateLocationAnalytics(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": startDate,
		"end_date":   endDate,
		"data":       locationData,
	})
}

// DeviceAnalyticsHandler provides device and platform analytics
func (eas *EnhancedAnalyticsService) DeviceAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)

	deviceData, err := eas.calculateDeviceAnalytics(projectID, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"start_date": startDate,
		"end_date":   endDate,
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
		"project_id":     projectID,
		"risk_threshold": threshold,
		"at_risk_users":  churnData["at_risk_users"],
		"churn_stats":    churnData["churn_stats"],
		"risk_breakdown": churnData["risk_breakdown"],
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
	// Query LocationAnalytics from database
	var locationData []LocationAnalytics
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("sessions DESC").
		Find(&locationData).Error

	if err != nil {
		return nil, err
	}

	// Aggregate by country
	countryMap := make(map[string]*LocationAnalytics)
	cityMap := make(map[string]*LocationAnalytics)

	for _, data := range locationData {
		// Group by country
		if existing, ok := countryMap[data.Country]; ok {
			existing.Sessions += data.Sessions
			existing.Users += data.Users
			existing.PageViews += data.PageViews
			existing.Revenue += data.Revenue
		} else {
			dataCopy := data
			countryMap[data.Country] = &dataCopy
		}

		// Store city data
		cityKey := data.City + "," + data.Country
		cityMap[cityKey] = &data
	}

	// Convert to sorted slices
	byCountry := make([]map[string]interface{}, 0)
	for _, data := range countryMap {
		byCountry = append(byCountry, map[string]interface{}{
			"country":         data.Country,
			"country_code":    data.CountryCode,
			"sessions":        data.Sessions,
			"users":           data.Users,
			"page_views":      data.PageViews,
			"bounce_rate":     data.BounceRate,
			"conversion_rate": data.ConversionRate,
			"revenue":         float64(data.Revenue) / 100, // Convert cents to dollars
		})
	}

	byCity := make([]map[string]interface{}, 0)
	for _, data := range cityMap {
		byCity = append(byCity, map[string]interface{}{
			"city":            data.City,
			"country":         data.Country,
			"country_code":    data.CountryCode,
			"sessions":        data.Sessions,
			"users":           data.Users,
			"page_views":      data.PageViews,
			"bounce_rate":     data.BounceRate,
			"conversion_rate": data.ConversionRate,
			"revenue":         float64(data.Revenue) / 100,
		})
	}

	// Sort by sessions
	sortByField := func(data []map[string]interface{}, field string) {
		for i := 0; i < len(data)-1; i++ {
			for j := i + 1; j < len(data); j++ {
				if data[i][field].(int) < data[j][field].(int) {
					data[i], data[j] = data[j], data[i]
				}
			}
		}
	}
	sortByField(byCountry, "sessions")
	sortByField(byCity, "sessions")

	// Calculate summary stats
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

	locationStats := map[string]interface{}{
		"by_country": byCountry,
		"by_city":    byCity,
		"summary": map[string]interface{}{
			"total_countries": totalCountries,
			"total_cities":    totalCities,
			"top_country":     topCountry,
			"top_city":        topCity,
		},
	}

	return locationStats, nil
}

// calculateDeviceAnalytics calculates device and platform analytics
func (eas *EnhancedAnalyticsService) calculateDeviceAnalytics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Query DeviceAnalytics from database
	var deviceData []DeviceAnalytics
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("sessions DESC").
		Find(&deviceData).Error

	if err != nil {
		return nil, err
	}

	// Aggregate by device type, OS, and browser
	deviceMap := make(map[string]*DeviceAnalytics)
	osMap := make(map[string]*DeviceAnalytics)
	browserMap := make(map[string]*DeviceAnalytics)

	for _, data := range deviceData {
		// Group by device
		if existing, ok := deviceMap[data.Device]; ok {
			existing.Sessions += data.Sessions
			existing.Users += data.Users
			existing.PageViews += data.PageViews
			existing.BounceRate = (existing.BounceRate + data.BounceRate) / 2
			existing.AvgSessionTime = (existing.AvgSessionTime + data.AvgSessionTime) / 2
			existing.ConversionRate = (existing.ConversionRate + data.ConversionRate) / 2
		} else {
			dataCopy := data
			deviceMap[data.Device] = &dataCopy
		}

		// Group by OS
		if existing, ok := osMap[data.OS]; ok {
			existing.Sessions += data.Sessions
			existing.Users += data.Users
			existing.ConversionRate = (existing.ConversionRate + data.ConversionRate) / 2
		} else {
			dataCopy := data
			osMap[data.OS] = &dataCopy
		}

		// Group by browser
		if existing, ok := browserMap[data.Browser]; ok {
			existing.Sessions += data.Sessions
			existing.Users += data.Users
		} else {
			dataCopy := data
			browserMap[data.Browser] = &dataCopy
		}
	}

	// Convert to slices
	byDevice := make([]map[string]interface{}, 0)
	for _, data := range deviceMap {
		byDevice = append(byDevice, map[string]interface{}{
			"device":           data.Device,
			"sessions":         data.Sessions,
			"users":            data.Users,
			"bounce_rate":      data.BounceRate,
			"avg_session_time": data.AvgSessionTime,
			"conversion_rate":  data.ConversionRate,
		})
	}

	byOS := make([]map[string]interface{}, 0)
	for _, data := range osMap {
		byOS = append(byOS, map[string]interface{}{
			"os":              data.OS,
			"sessions":        data.Sessions,
			"users":           data.Users,
			"conversion_rate": data.ConversionRate,
		})
	}

	byBrowser := make([]map[string]interface{}, 0)
	for _, data := range browserMap {
		byBrowser = append(byBrowser, map[string]interface{}{
			"browser":  data.Browser,
			"sessions": data.Sessions,
			"users":    data.Users,
		})
	}

	// Sort by sessions
	sortByField := func(data []map[string]interface{}, field string) {
		for i := 0; i < len(data)-1; i++ {
			for j := i + 1; j < len(data); j++ {
				if data[i][field].(int) < data[j][field].(int) {
					data[i], data[j] = data[j], data[i]
				}
			}
		}
	}
	sortByField(byDevice, "sessions")
	sortByField(byOS, "sessions")
	sortByField(byBrowser, "sessions")

	deviceStats := map[string]interface{}{
		"by_device":  byDevice,
		"by_os":      byOS,
		"by_browser": byBrowser,
	}

	return deviceStats, nil
}

// calculateRetentionCohorts calculates user retention by signup cohorts
func (eas *EnhancedAnalyticsService) calculateRetentionCohorts(projectID string, months int) ([]map[string]interface{}, error) {
	// Query UserCohortMetrics from database
	startDate := time.Now().AddDate(0, -months, 0)

	var cohortData []UserCohortMetrics
	err := eas.db.Where("project_id = ? AND cohort_month >= ?", projectID, startDate).
		Order("cohort_month DESC, period_number ASC").
		Find(&cohortData).Error

	if err != nil {
		return nil, err
	}

	// Group by cohort month
	cohortMap := make(map[string]map[string]interface{})

	for _, data := range cohortData {
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

	// Convert to slice
	cohorts := make([]map[string]interface{}, 0)
	for _, cohort := range cohortMap {
		cohorts = append(cohorts, cohort)
	}

	// Sort by cohort month (newest first)
	for i := 0; i < len(cohorts)-1; i++ {
		for j := i + 1; j < len(cohorts); j++ {
			if cohorts[i]["cohort_month"].(string) < cohorts[j]["cohort_month"].(string) {
				cohorts[i], cohorts[j] = cohorts[j], cohorts[i]
			}
		}
	}

	return cohorts, nil
}

// calculateFeatureAdoption calculates feature adoption metrics
func (eas *EnhancedAnalyticsService) calculateFeatureAdoption(projectID string, start, end time.Time) ([]map[string]interface{}, error) {
	// Query FeatureAdoption from database
	var featureData []FeatureAdoption
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("adoption_rate DESC").
		Find(&featureData).Error

	if err != nil {
		return nil, err
	}

	// Group by feature name and aggregate
	featureMap := make(map[string]*FeatureAdoption)

	for _, data := range featureData {
		if existing, ok := featureMap[data.FeatureName]; ok {
			// Aggregate multiple days
			existing.TotalUsers += data.TotalUsers
			existing.UsersWhoTriedFeature += data.UsersWhoTriedFeature
			existing.DailyActiveFeature += data.DailyActiveFeature
			existing.WeeklyActiveFeature += data.WeeklyActiveFeature
			existing.MonthlyActiveFeature += data.MonthlyActiveFeature
			existing.AdoptionRate = (existing.AdoptionRate + data.AdoptionRate) / 2
			existing.FeatureStickiness = (existing.FeatureStickiness + data.FeatureStickiness) / 2
			existing.TimeToFirstUse = (existing.TimeToFirstUse + data.TimeToFirstUse) / 2
			existing.DropoffAfterFirstUse = (existing.DropoffAfterFirstUse + data.DropoffAfterFirstUse) / 2
		} else {
			dataCopy := data
			featureMap[data.FeatureName] = &dataCopy
		}
	}

	// Convert to slice
	features := make([]map[string]interface{}, 0)
	for _, data := range featureMap {
		features = append(features, map[string]interface{}{
			"feature_name":        data.FeatureName,
			"total_users":         data.TotalUsers,
			"adopted_users":       data.UsersWhoTriedFeature,
			"adoption_rate":       data.AdoptionRate,
			"daily_active":        data.DailyActiveFeature,
			"weekly_active":       data.WeeklyActiveFeature,
			"monthly_active":      data.MonthlyActiveFeature,
			"stickiness":          data.FeatureStickiness,
			"time_to_first_use":   data.TimeToFirstUse,
			"dropoff_after_first": data.DropoffAfterFirstUse,
		})
	}

	// Sort by adoption rate
	for i := 0; i < len(features)-1; i++ {
		for j := i + 1; j < len(features); j++ {
			if features[i]["adoption_rate"].(float64) < features[j]["adoption_rate"].(float64) {
				features[i], features[j] = features[j], features[i]
			}
		}
	}

	return features, nil
}

// calculateChurnRisk calculates churn risk scores and at-risk users
func (eas *EnhancedAnalyticsService) calculateChurnRisk(projectID string, threshold float64) (map[string]interface{}, error) {
	// Query ChurnAnalytics from database
	var churnData []ChurnAnalytics
	err := eas.db.Where("project_id = ? AND risk_score >= ?", projectID, threshold).
		Order("risk_score DESC").
		Find(&churnData).Error

	if err != nil {
		return nil, err
	}

	// Aggregate metrics
	totalUsers := len(churnData)
	atRiskUsers := 0
	churnedUsers := 0
	criticalRisk := 0
	highRisk := 0
	mediumRisk := 0
	totalRevenue := 0.0

	for _, data := range churnData {
		if !data.IsChurned {
			atRiskUsers++
		} else {
			churnedUsers++
		}
		totalRevenue += float64(data.SubscriptionValue)

		if data.ChurnRiskScore >= 80 {
			criticalRisk++
		} else if data.ChurnRiskScore >= 60 {
			highRisk++
		} else if data.ChurnRiskScore >= 40 {
			mediumRisk++
		}
	}

	churnRate := 0.0
	if totalUsers > 0 {
		churnRate = (float64(churnedUsers) / float64(totalUsers)) * 100
	}

	churnStats := map[string]interface{}{
		"total_users":         totalUsers,
		"at_risk_users":       atRiskUsers,
		"critical_risk":       criticalRisk,
		"high_risk":           highRisk,
		"medium_risk":         mediumRisk,
		"churn_rate_30d":      churnRate,
		"predicted_churn_30d": atRiskUsers,
		"revenue_at_risk":     totalRevenue,
	}

	// Get at-risk user details (sample)
	atRiskUsersList := make([]map[string]interface{}, 0)
	for i, data := range churnData {
		if i >= 10 { // Limit to 10 users
			break
		}
		atRiskUsersList = append(atRiskUsersList, map[string]interface{}{
			"user_id":                data.UserID,
			"churn_risk_score":       data.ChurnRiskScore,
			"risk_category":          data.ChurnRiskCategory,
			"days_since_last_active": data.DaysSinceLastActive,
			"feature_usage_score":    data.FeatureUsageScore,
			"subscription_value":     data.SubscriptionValue,
		})
	}

	riskBreakdown := map[string]interface{}{
		"by_risk_category": []map[string]interface{}{
			{"category": "Critical (80-100)", "count": criticalRisk},
			{"category": "High (60-79)", "count": highRisk},
			{"category": "Medium (40-59)", "count": mediumRisk},
		},
	}

	return map[string]interface{}{
		"at_risk_users":  atRiskUsersList,
		"churn_stats":    churnStats,
		"risk_breakdown": riskBreakdown,
	}, nil
}

// calculateConversionFunnel calculates conversion funnel metrics
func (eas *EnhancedAnalyticsService) calculateConversionFunnel(projectID string, funnelName string, start, end time.Time) ([]map[string]interface{}, error) {
	// Query ConversionFunnel from database
	var funnelData []ConversionFunnel
	query := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end)

	if funnelName != "" {
		query = query.Where("funnel_name = ?", funnelName)
	}

	err := query.Order("step_number ASC").Find(&funnelData).Error
	if err != nil {
		return nil, err
	}

	// Group by step number and aggregate
	stepMap := make(map[int]*ConversionFunnel)

	for _, data := range funnelData {
		if existing, ok := stepMap[data.StepNumber]; ok {
			// Aggregate multiple days
			existing.Users += data.Users
			existing.Revenue += data.Revenue
			existing.ConversionRate = (existing.ConversionRate + data.ConversionRate) / 2
			existing.DropoffRate = (existing.DropoffRate + data.DropoffRate) / 2
			existing.AvgTimeInStep = (existing.AvgTimeInStep + data.AvgTimeInStep) / 2
		} else {
			dataCopy := data
			stepMap[data.StepNumber] = &dataCopy
		}
	}

	// Convert to slice
	funnelSteps := make([]map[string]interface{}, 0)
	for _, data := range stepMap {
		funnelSteps = append(funnelSteps, map[string]interface{}{
			"step_number":      data.StepNumber,
			"step_name":        data.StepName,
			"users":            data.Users,
			"conversion_rate":  data.ConversionRate,
			"dropoff_rate":     data.DropoffRate,
			"avg_time_in_step": data.AvgTimeInStep,
			"revenue":          data.Revenue,
		})
	}

	// Sort by step number
	for i := 0; i < len(funnelSteps)-1; i++ {
		for j := i + 1; j < len(funnelSteps); j++ {
			if funnelSteps[i]["step_number"].(int) > funnelSteps[j]["step_number"].(int) {
				funnelSteps[i], funnelSteps[j] = funnelSteps[j], funnelSteps[i]
			}
		}
	}

	return funnelSteps, nil
}

// calculateSessionMetrics calculates enhanced session analytics
func (eas *EnhancedAnalyticsService) calculateSessionMetrics(projectID string, start, end time.Time) (map[string]interface{}, error) {
	// Query UserSessionMetrics from database
	var sessionMetrics []UserSessionMetrics
	err := eas.db.Where("project_id = ? AND date BETWEEN ? AND ?", projectID, start, end).
		Order("date DESC").
		Find(&sessionMetrics).Error

	if err != nil {
		return nil, err
	}

	// Aggregate metrics
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
	count := len(sessionMetrics)

	timeSeries := make([]map[string]interface{}, 0)

	for _, data := range sessionMetrics {
		totalSessions += data.TotalSessions
		dau += data.DAU
		wau += data.WAU
		mau += data.MAU
		totalDuration += data.AvgSessionDuration * data.TotalSessions
		bounceRateSum += data.BounceRate
		returnRateSum += data.ReturnUserRate
		stickinessSum += data.StickinessRatio
		sessionFreqSum += data.AvgSessionsPerUser

		// Build time series (limit to last 30 days)
		if len(timeSeries) < 30 {
			timeSeries = append(timeSeries, map[string]interface{}{
				"date":         data.Date.Format("2006-01-02"),
				"sessions":     data.TotalSessions,
				"users":        data.DAU,
				"avg_duration": data.AvgSessionDuration,
			})
		}
	}

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
	if count > 0 {
		avgBounceRate = bounceRateSum / float64(count)
		avgReturnRate = returnRateSum / float64(count)
		avgStickiness = stickinessSum / float64(count)
		avgSessionFreq = sessionFreqSum / float64(count)
		// Use average DAU/WAU/MAU
		dau = dau / count
		wau = wau / count
		mau = mau / count
	}

	sessionData := map[string]interface{}{
		"overview": map[string]interface{}{
			"total_sessions":       totalSessions,
			"unique_users":         uniqueUsers,
			"avg_session_duration": avgDuration,
			"bounce_rate":          avgBounceRate,
			"return_visitor_rate":  avgReturnRate,
		},
		"engagement": map[string]interface{}{
			"dau":               dau,
			"wau":               wau,
			"mau":               mau,
			"stickiness_ratio":  avgStickiness,
			"session_frequency": avgSessionFreq,
		},
		"time_series": timeSeries,
	}

	return sessionData, nil
}
