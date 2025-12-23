package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// FeatureUsageStats represents aggregated feature usage statistics
type FeatureUsageStats struct {
	FeatureName        string    `json:"feature_name"`
	TotalUsers         int       `json:"total_users"`
	UniqueUsers        int       `json:"unique_users"`
	TotalUsages        int       `json:"total_usages"`
	FirstUsed          time.Time `json:"first_used"`
	LastUsed           time.Time `json:"last_used"`
	AvgUsagePerUser    float64   `json:"avg_usage_per_user"`
	AdoptionRate       float64   `json:"adoption_rate"`        // % of total users who used this feature
	DailyActiveUsers   int       `json:"daily_active_users"`   // Users who used it today
	WeeklyActiveUsers  int       `json:"weekly_active_users"`  // Users who used it this week
	MonthlyActiveUsers int       `json:"monthly_active_users"` // Users who used it this month
	RetentionRate      float64   `json:"retention_rate"`       // % who used it again after first use
}

// OnboardingStep represents a step in the onboarding process
type OnboardingStep struct {
	StepName          string    `json:"step_name"`
	StepIndex         int       `json:"step_index"`
	UsersReached      int       `json:"users_reached"`
	UsersCompleted    int       `json:"users_completed"`
	CompletionRate    float64   `json:"completion_rate"`
	AvgTimeToComplete float64   `json:"avg_time_to_complete"` // in seconds
	DropoffRate       float64   `json:"dropoff_rate"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
}

// OnboardingFunnelStats represents the complete onboarding funnel
type OnboardingFunnelStats struct {
	FunnelName        string           `json:"funnel_name"`
	TotalStarted      int              `json:"total_started"`
	TotalCompleted    int              `json:"total_completed"`
	CompletionRate    float64          `json:"completion_rate"`
	AvgCompletionTime float64          `json:"avg_completion_time"` // in seconds
	Steps             []OnboardingStep `json:"steps"`
	DropoffPoints     []string         `json:"dropoff_points"` // Steps with highest dropoff
}

// UserFeatureJourney represents a user's interaction with features over time
type UserFeatureJourney struct {
	UserID           string         `json:"user_id"`
	Email            string         `json:"email,omitempty"`
	SignupDate       time.Time      `json:"signup_date"`
	FeaturesUsed     []string       `json:"features_used"`
	FeatureCount     int            `json:"feature_count"`
	LastActivity     time.Time      `json:"last_activity"`
	OnboardingStatus string         `json:"onboarding_status"` // completed, in_progress, abandoned
	EngagementScore  int            `json:"engagement_score"`  // 0-100
	FeatureUsageMap  map[string]int `json:"feature_usage_map"` // feature -> usage count
}

// getFeatureUsageHandler returns feature usage statistics
// OPTIMIZED: Uses SQL aggregation instead of loading all events into memory
func (s *Server) getFeatureUsageHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	featureName := c.Query("feature")

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24 * time.Hour) // Include the end date

	now := time.Now()
	oneDayAgo := now.Add(-24 * time.Hour)
	oneWeekAgo := now.Add(-7 * 24 * time.Hour)
	oneMonthAgo := now.Add(-30 * 24 * time.Hour)

	// Get total unique users in the period
	var totalUsers int64
	s.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ?", accountID.(string), projectID).
		Where("timestamp >= ? AND timestamp < ?", start, end).
		Distinct("user_id").
		Count(&totalUsers)

	// OPTIMIZED: Use SQL aggregation to get feature stats directly
	type FeatureAggregation struct {
		FeatureName string    `gorm:"column:feature_name"`
		UniqueUsers int64     `gorm:"column:unique_users"`
		TotalUsages int64     `gorm:"column:total_usages"`
		FirstUsed   time.Time `gorm:"column:first_used"`
		LastUsed    time.Time `gorm:"column:last_used"`
		DAU         int64     `gorm:"column:dau"`
		WAU         int64     `gorm:"column:wau"`
		MAU         int64     `gorm:"column:mau"`
		ReturnUsers int64     `gorm:"column:return_users"`
	}

	// Build the aggregation query
	query := `
		WITH feature_events AS (
			SELECT 
				properties->>'feature_name' as feature_name,
				user_id,
				timestamp,
				ROW_NUMBER() OVER (PARTITION BY properties->>'feature_name', user_id ORDER BY timestamp) as rn
			FROM events
			WHERE account_id = $1 
				AND project_id = $2
				AND timestamp >= $3 AND timestamp < $4
				AND event_type = 'feature_usage'
				AND properties->>'feature_name' IS NOT NULL
				AND properties->>'feature_name' != ''
	`

	args := []interface{}{accountID.(string), projectID, start, end}
	argIndex := 5

	if featureName != "" {
		query += ` AND properties->>'feature_name' = $` + string('0'+byte(argIndex))
		args = append(args, featureName)
		argIndex++
	}

	query += `
		)
		SELECT 
			feature_name,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(*) as total_usages,
			MIN(timestamp) as first_used,
			MAX(timestamp) as last_used,
			COUNT(DISTINCT CASE WHEN timestamp > $` + string('0'+byte(argIndex)) + ` THEN user_id END) as dau,
			COUNT(DISTINCT CASE WHEN timestamp > $` + string('0'+byte(argIndex+1)) + ` THEN user_id END) as wau,
			COUNT(DISTINCT CASE WHEN timestamp > $` + string('0'+byte(argIndex+2)) + ` THEN user_id END) as mau,
			COUNT(DISTINCT CASE WHEN rn > 1 THEN user_id END) as return_users
		FROM feature_events
		GROUP BY feature_name
		ORDER BY total_usages DESC
	`
	args = append(args, oneDayAgo, oneWeekAgo, oneMonthAgo)

	var featureAggs []FeatureAggregation
	if err := s.db.Raw(query, args...).Scan(&featureAggs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch feature usage data"})
		return
	}

	// Build response
	var results []FeatureUsageStats
	for _, agg := range featureAggs {
		stats := FeatureUsageStats{
			FeatureName:        agg.FeatureName,
			UniqueUsers:        int(agg.UniqueUsers),
			TotalUsers:         int(totalUsers),
			TotalUsages:        int(agg.TotalUsages),
			FirstUsed:          agg.FirstUsed,
			LastUsed:           agg.LastUsed,
			DailyActiveUsers:   int(agg.DAU),
			WeeklyActiveUsers:  int(agg.WAU),
			MonthlyActiveUsers: int(agg.MAU),
		}

		// Calculate derived metrics
		if stats.UniqueUsers > 0 {
			stats.AvgUsagePerUser = float64(stats.TotalUsages) / float64(stats.UniqueUsers)
			stats.RetentionRate = float64(agg.ReturnUsers) / float64(stats.UniqueUsers) * 100
		}

		if totalUsers > 0 {
			stats.AdoptionRate = float64(stats.UniqueUsers) / float64(totalUsers) * 100
		}

		results = append(results, stats)
	}

	c.JSON(http.StatusOK, gin.H{
		"features":    results,
		"total_users": totalUsers,
		"date_range": gin.H{
			"start": startDate,
			"end":   endDate,
		},
	})
}

// getOnboardingStatsHandler returns onboarding funnel statistics
func (s *Server) getOnboardingStatsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Parse query parameters
	startDate := c.DefaultQuery("start_date", time.Now().AddDate(0, 0, -30).Format("2006-01-02"))
	endDate := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))
	funnelName := c.DefaultQuery("funnel", "onboarding")

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24 * time.Hour)

	// Fetch onboarding events
	var events []Event
	if err := s.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ?", accountID.(string), projectID).
		Where("timestamp >= ? AND timestamp < ?", start, end).
		Where("event_type IN ?", []string{"onboarding_started", "onboarding_step_completed", "onboarding_completed"}).
		Order("timestamp ASC").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch onboarding data"})
		return
	}

	// Track user journeys through onboarding
	type UserOnboarding struct {
		StartTime      time.Time
		CompletedSteps map[string]time.Time
		Completed      bool
		CompletionTime time.Time
	}

	userJourneys := make(map[string]*UserOnboarding)
	stepStats := make(map[string]*OnboardingStep)
	stepOrder := make(map[string]int)
	allSteps := make(map[string]bool)

	for _, event := range events {
		if event.UserID == "" {
			continue
		}

		// Initialize user journey
		if _, exists := userJourneys[event.UserID]; !exists {
			userJourneys[event.UserID] = &UserOnboarding{
				CompletedSteps: make(map[string]time.Time),
			}
		}

		journey := userJourneys[event.UserID]

		switch event.EventType {
		case "onboarding_started":
			journey.StartTime = event.Timestamp

		case "onboarding_step_completed":
			stepName, _ := event.Properties["step_name"].(string)
			if stepName == "" {
				continue
			}

			stepIndex := 0
			if idx, ok := event.Properties["step_index"].(float64); ok {
				stepIndex = int(idx)
			}

			journey.CompletedSteps[stepName] = event.Timestamp
			allSteps[stepName] = true
			stepOrder[stepName] = stepIndex

			// Initialize step stats
			if _, exists := stepStats[stepName]; !exists {
				stepStats[stepName] = &OnboardingStep{
					StepName:  stepName,
					StepIndex: stepIndex,
					FirstSeen: event.Timestamp,
					LastSeen:  event.Timestamp,
				}
			}

			stats := stepStats[stepName]
			stats.UsersCompleted++

			if event.Timestamp.Before(stats.FirstSeen) {
				stats.FirstSeen = event.Timestamp
			}
			if event.Timestamp.After(stats.LastSeen) {
				stats.LastSeen = event.Timestamp
			}

		case "onboarding_completed":
			journey.Completed = true
			journey.CompletionTime = event.Timestamp
		}
	}

	// Calculate statistics
	totalStarted := len(userJourneys)
	totalCompleted := 0
	var completionTimes []float64

	for _, journey := range userJourneys {
		if journey.Completed {
			totalCompleted++
			if !journey.StartTime.IsZero() {
				duration := journey.CompletionTime.Sub(journey.StartTime).Seconds()
				completionTimes = append(completionTimes, duration)
			}
		}
	}

	// Calculate completion rate
	completionRate := 0.0
	if totalStarted > 0 {
		completionRate = float64(totalCompleted) / float64(totalStarted) * 100
	}

	// Calculate average completion time
	avgCompletionTime := 0.0
	if len(completionTimes) > 0 {
		sum := 0.0
		for _, t := range completionTimes {
			sum += t
		}
		avgCompletionTime = sum / float64(len(completionTimes))
	}

	// Calculate step-by-step statistics
	for stepName, stats := range stepStats {
		// Count users who reached this step
		usersReached := 0
		var timesToComplete []float64

		for _, journey := range userJourneys {
			if !journey.StartTime.IsZero() {
				usersReached++ // They started onboarding

				if completionTime, completed := journey.CompletedSteps[stepName]; completed {
					duration := completionTime.Sub(journey.StartTime).Seconds()
					timesToComplete = append(timesToComplete, duration)
				}
			}
		}

		stats.UsersReached = usersReached

		if usersReached > 0 {
			stats.CompletionRate = float64(stats.UsersCompleted) / float64(usersReached) * 100
			stats.DropoffRate = 100 - stats.CompletionRate
		}

		if len(timesToComplete) > 0 {
			sum := 0.0
			for _, t := range timesToComplete {
				sum += t
			}
			stats.AvgTimeToComplete = sum / float64(len(timesToComplete))
		}
	}

	// Convert to sorted slice
	var steps []OnboardingStep
	for _, stats := range stepStats {
		steps = append(steps, *stats)
	}

	// Sort by step index
	for i := 0; i < len(steps); i++ {
		for j := i + 1; j < len(steps); j++ {
			if steps[i].StepIndex > steps[j].StepIndex {
				steps[i], steps[j] = steps[j], steps[i]
			}
		}
	}

	// Find dropoff points (steps with highest dropoff rate)
	var dropoffPoints []string
	for _, step := range steps {
		if step.DropoffRate > 30 { // More than 30% dropoff
			dropoffPoints = append(dropoffPoints, step.StepName)
		}
	}

	funnelStats := OnboardingFunnelStats{
		FunnelName:        funnelName,
		TotalStarted:      totalStarted,
		TotalCompleted:    totalCompleted,
		CompletionRate:    completionRate,
		AvgCompletionTime: avgCompletionTime,
		Steps:             steps,
		DropoffPoints:     dropoffPoints,
	}

	c.JSON(http.StatusOK, funnelStats)
}

// getUserFeatureJourneyHandler returns a specific user's feature journey
func (s *Server) getUserFeatureJourneyHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	userID := c.Param("user_id")

	if projectID == "" || userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and User ID are required"})
		return
	}

	accountID, _ := c.Get("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Fetch all events for this user
	var events []Event
	if err := s.db.Model(&Event{}).
		Where("account_id = ? AND project_id = ? AND user_id = ?", accountID.(string), projectID, userID).
		Order("timestamp ASC").
		Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user journey"})
		return
	}

	if len(events) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Build journey
	journey := UserFeatureJourney{
		UserID:          userID,
		SignupDate:      events[0].Timestamp,
		LastActivity:    events[len(events)-1].Timestamp,
		FeatureUsageMap: make(map[string]int),
	}

	featuresUsed := make(map[string]bool)
	onboardingCompleted := false
	activityCount := 0

	for _, event := range events {
		if event.Email != "" && journey.Email == "" {
			journey.Email = event.Email
		}

		if event.EventType == "feature_usage" {
			if feature, ok := event.Properties["feature_name"].(string); ok {
				featuresUsed[feature] = true
				journey.FeatureUsageMap[feature]++
				activityCount++
			}
		}

		if event.EventType == "onboarding_completed" {
			onboardingCompleted = true
		}
	}

	// Convert features map to slice
	for feature := range featuresUsed {
		journey.FeaturesUsed = append(journey.FeaturesUsed, feature)
	}
	journey.FeatureCount = len(journey.FeaturesUsed)

	// Determine onboarding status
	if onboardingCompleted {
		journey.OnboardingStatus = "completed"
	} else {
		// Check if they've had recent activity
		daysSinceLastActivity := time.Since(journey.LastActivity).Hours() / 24
		if daysSinceLastActivity > 7 {
			journey.OnboardingStatus = "abandoned"
		} else {
			journey.OnboardingStatus = "in_progress"
		}
	}

	// Calculate engagement score (0-100)
	// Based on: features used, activity count, recency
	engagementScore := 0
	engagementScore += journey.FeatureCount * 5 // Up to 50 points for features (10 features max)
	if engagementScore > 50 {
		engagementScore = 50
	}
	engagementScore += min(activityCount, 30) // Up to 30 points for activity

	// Recency bonus (up to 20 points)
	daysSinceActivity := time.Since(journey.LastActivity).Hours() / 24
	if daysSinceActivity < 1 {
		engagementScore += 20
	} else if daysSinceActivity < 7 {
		engagementScore += 15
	} else if daysSinceActivity < 30 {
		engagementScore += 10
	}

	journey.EngagementScore = engagementScore

	c.JSON(http.StatusOK, journey)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
