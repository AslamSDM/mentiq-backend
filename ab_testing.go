package main

import (
	"crypto/md5"
	"encoding/json"
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"mentiq-backend/prisma/db"
)

// A/B Testing Request/Response Structures
type CreateExperimentRequest struct {
	Name         string                 `json:"name" binding:"required"`
	Description  string                 `json:"description"`
	Key          string                 `json:"key" binding:"required"`
	TrafficSplit float64                `json:"trafficSplit" binding:"required,min=0,max=1"`
	StartDate    string                 `json:"startDate"`
	EndDate      string                 `json:"endDate"`
	Variants     []CreateVariantRequest `json:"variants" binding:"required,min=2"`
}

type CreateVariantRequest struct {
	Name         string  `json:"name" binding:"required"`
	Key          string  `json:"key" binding:"required"`
	Description  string  `json:"description"`
	IsControl    bool    `json:"isControl"`
	TrafficSplit float64 `json:"trafficSplit" binding:"required,min=0,max=1"`
}

type GetExperimentRequest struct {
	UserId      string `json:"userId"`
	AnonymousId string `json:"anonymousId"`
}

type TrackConversionRequest struct {
	ExperimentId string                 `json:"experimentId" binding:"required"`
	UserId       string                 `json:"userId"`
	AnonymousId  string                 `json:"anonymousId"`
	EventName    string                 `json:"eventName" binding:"required"`
	EventValue   float64                `json:"eventValue"`
	Properties   map[string]interface{} `json:"properties"`
}

type ExperimentResponse struct {
	Id           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Key          string            `json:"key"`
	Status       string            `json:"status"`
	TrafficSplit float64           `json:"trafficSplit"`
	StartDate    *time.Time        `json:"startDate"`
	EndDate      *time.Time        `json:"endDate"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	Variants     []VariantResponse `json:"variants"`
}

type VariantResponse struct {
	Id           string    `json:"id"`
	Name         string    `json:"name"`
	Key          string    `json:"key"`
	Description  string    `json:"description"`
	IsControl    bool      `json:"isControl"`
	TrafficSplit float64   `json:"trafficSplit"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type AssignmentResponse struct {
	ExperimentId string             `json:"experimentId"`
	VariantId    string             `json:"variantId"`
	VariantKey   string             `json:"variantKey"`
	VariantName  string             `json:"variantName"`
	IsControl    bool               `json:"isControl"`
	AssignedAt   time.Time          `json:"assignedAt"`
	Experiment   ExperimentResponse `json:"experiment"`
}

// A/B Testing Service Methods
func (s *Server) CreateExperiment(c *gin.Context) {
	var req CreateExperimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	projectId, _ := c.Get("project_id")

	// Validate traffic split sums to 1.0
	totalSplit := 0.0
	for _, variant := range req.Variants {
		totalSplit += variant.TrafficSplit
	}
	if totalSplit < 0.99 || totalSplit > 1.01 { // Allow small floating point errors
		c.JSON(http.StatusBadRequest, gin.H{"error": "Variant traffic splits must sum to 1.0"})
		return
	}

	// Parse dates
	var startDate, endDate *time.Time
	if req.StartDate != "" {
		parsed, err := time.Parse(time.RFC3339, req.StartDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid startDate format"})
			return
		}
		startDate = &parsed
	}
	if req.EndDate != "" {
		parsed, err := time.Parse(time.RFC3339, req.EndDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid endDate format"})
			return
		}
		endDate = &parsed
	}

	// Create experiment
	experiment, err := s.client.Experiment.CreateOne(
		db.Experiment.Name.Set(req.Name),
		db.Experiment.Description.Set(req.Description),
		db.Experiment.Key.Set(req.Key),
		db.Experiment.TrafficSplit.Set(req.TrafficSplit),
		db.Experiment.StartDate.Set(*startDate),
		db.Experiment.EndDate.Set(*endDate),
		db.Experiment.Project.Link(db.Project.ID.Equals(projectId.(string))),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create experiment"})
		return
	}

	// Create variants
	for _, variantReq := range req.Variants {
		_, err := s.client.Variant.CreateOne(
			db.Variant.Name.Set(variantReq.Name),
			db.Variant.Key.Set(variantReq.Key),
			db.Variant.Description.Set(variantReq.Description),
			db.Variant.IsControl.Set(variantReq.IsControl),
			db.Variant.TrafficSplit.Set(variantReq.TrafficSplit),
			db.Variant.Experiment.Link(db.Experiment.ID.Equals(experiment.ID)),
		).Exec(c.Request.Context())

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create variant"})
			return
		}
	}

	c.JSON(http.StatusCreated, gin.H{"experimentId": experiment.ID})
}

func (s *Server) GetExperiments(c *gin.Context) {
	projectId, _ := c.Get("project_id")

	experiments, err := s.client.Experiment.FindMany(
		db.Experiment.Project.Where(db.Project.ID.Equals(projectId.(string))),
	).With(
		db.Experiment.Variants.Fetch(),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch experiments"})
		return
	}

	var responses []ExperimentResponse
	for _, exp := range experiments {
		var variants []VariantResponse
		for _, variant := range exp.Variants() {
			variants = append(variants, VariantResponse{
				Id:           variant.ID,
				Name:         variant.Name,
				Key:          variant.Key,
				Description:  variant.Description,
				IsControl:    variant.IsControl,
				TrafficSplit: variant.TrafficSplit,
				CreatedAt:    variant.CreatedAt,
				UpdatedAt:    variant.UpdatedAt,
			})
		}

		responses = append(responses, ExperimentResponse{
			Id:           exp.ID,
			Name:         exp.Name,
			Description:  exp.Description,
			Key:          exp.Key,
			Status:       string(exp.Status),
			TrafficSplit: exp.TrafficSplit,
			StartDate:    &exp.StartDate,
			EndDate:      &exp.EndDate,
			CreatedAt:    exp.CreatedAt,
			UpdatedAt:    exp.UpdatedAt,
			Variants:     variants,
		})
	}

	c.JSON(http.StatusOK, responses)
}

func (s *Server) GetExperiment(c *gin.Context) {
	experimentId := c.Param("id")
	projectId, _ := c.Get("project_id")

	experiment, err := s.client.Experiment.FindUnique(
		db.Experiment.ID.Equals(experimentId),
	).With(
		db.Experiment.Variants.Fetch(),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
		return
	}

	if experiment.ProjectID != projectId.(string) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	var variants []VariantResponse
	for _, variant := range experiment.Variants() {
		variants = append(variants, VariantResponse{
			Id:           variant.ID,
			Name:         variant.Name,
			Key:          variant.Key,
			Description:  variant.Description,
			IsControl:    variant.IsControl,
			TrafficSplit: variant.TrafficSplit,
			CreatedAt:    variant.CreatedAt,
			UpdatedAt:    variant.UpdatedAt,
		})
	}

	response := ExperimentResponse{
		Id:           experiment.ID,
		Name:         experiment.Name,
		Description:  experiment.Description,
		Key:          experiment.Key,
		Status:       string(experiment.Status),
		TrafficSplit: experiment.TrafficSplit,
		StartDate:    &experiment.StartDate,
		EndDate:      &experiment.EndDate,
		CreatedAt:    experiment.CreatedAt,
		UpdatedAt:    experiment.UpdatedAt,
		Variants:     variants,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) GetAssignment(c *gin.Context) {
	var req GetExperimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	experimentKey := c.Param("experimentKey")
	projectId, _ := c.Get("project_id")

	// Find experiment
	experiment, err := s.client.Experiment.FindFirst(
		db.Experiment.Key.Equals(experimentKey),
		db.Experiment.Project.Where(db.Project.ID.Equals(projectId.(string))),
		db.Experiment.Status.Equals(db.ExperimentStatusRUNNING),
	).With(
		db.Experiment.Variants.Fetch(),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found or not running"})
		return
	}

	// Check if user already has an assignment
	var existingAssignment *db.ExperimentAssignmentModel
	if req.UserId != "" {
		assignment, err := s.client.ExperimentAssignment.FindUnique(
			db.ExperimentAssignment.UserIDExperimentID(
				db.ExperimentAssignment.UserID.Equals(req.UserId),
				db.ExperimentAssignment.ExperimentID.Equals(experiment.ID),
			),
		).With(
			db.ExperimentAssignment.Variant.Fetch(),
		).Exec(c.Request.Context())
		if err == nil {
			existingAssignment = assignment
		}
	} else if req.AnonymousId != "" {
		assignment, err := s.client.ExperimentAssignment.FindUnique(
			db.ExperimentAssignment.AnonymousIDExperimentID(
				db.ExperimentAssignment.AnonymousID.Equals(req.AnonymousId),
				db.ExperimentAssignment.ExperimentID.Equals(experiment.ID),
			),
		).With(
			db.ExperimentAssignment.Variant.Fetch(),
		).Exec(c.Request.Context())
		if err == nil {
			existingAssignment = assignment
		}
	}

	// Return existing assignment if found
	if existingAssignment != nil {
		variant := existingAssignment.Variant()
		response := AssignmentResponse{
			ExperimentId: existingAssignment.ExperimentID,
			VariantId:    existingAssignment.VariantID,
			VariantKey:   variant.Key,
			VariantName:  variant.Name,
			IsControl:    variant.IsControl,
			AssignedAt:   existingAssignment.AssignedAt,
		}
		c.JSON(http.StatusOK, response)
		return
	}

	// Check if user should be included in experiment
	userHash := md5.Sum([]byte(req.UserId + req.AnonymousId + experiment.ID))
	hashFloat := float64(userHash[0]) / 255.0

	if hashFloat > experiment.TrafficSplit {
		// User not included in experiment
		c.JSON(http.StatusOK, gin.H{"included": false})
		return
	}

	// Assign to variant based on traffic split
	variants := experiment.Variants()
	selectedVariant := s.selectVariant(variants, req.UserId+req.AnonymousId+experiment.ID)

	// Create assignment
	assignment, err := s.client.ExperimentAssignment.CreateOne(
		db.ExperimentAssignment.UserID.Set(req.UserId),
		db.ExperimentAssignment.AnonymousID.Set(req.AnonymousId),
		db.ExperimentAssignment.Variant.Link(db.Variant.ID.Equals(selectedVariant.ID)),
		db.ExperimentAssignment.Experiment.Link(db.Experiment.ID.Equals(experiment.ID)),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create assignment"})
		return
	}

	response := AssignmentResponse{
		ExperimentId: assignment.ExperimentID,
		VariantId:    assignment.VariantID,
		VariantKey:   selectedVariant.Key,
		VariantName:  selectedVariant.Name,
		IsControl:    selectedVariant.IsControl,
		AssignedAt:   assignment.AssignedAt,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) TrackConversion(c *gin.Context) {
	var req TrackConversionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Find user's assignment
	var assignment *db.ExperimentAssignmentModel
	var err error
	if req.UserId != "" {
		assignment, err = s.client.ExperimentAssignment.FindUnique(
			db.ExperimentAssignment.UserIDExperimentID(
				db.ExperimentAssignment.UserID.Equals(req.UserId),
				db.ExperimentAssignment.ExperimentID.Equals(req.ExperimentId),
			),
		).Exec(c.Request.Context())
	} else if req.AnonymousId != "" {
		assignment, err = s.client.ExperimentAssignment.FindUnique(
			db.ExperimentAssignment.AnonymousIDExperimentID(
				db.ExperimentAssignment.AnonymousID.Equals(req.AnonymousId),
				db.ExperimentAssignment.ExperimentID.Equals(req.ExperimentId),
			),
		).Exec(c.Request.Context())
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No assignment found for user"})
		return
	}

	// Create conversion event
	propertiesJson, _ := json.Marshal(req.Properties)
	_, err = s.client.ConversionEvent.CreateOne(
		db.ConversionEvent.Experiment.Link(db.Experiment.ID.Equals(req.ExperimentId)),
		db.ConversionEvent.Variant.Link(db.Variant.ID.Equals(assignment.VariantID)),
		db.ConversionEvent.UserID.Set(req.UserId),
		db.ConversionEvent.AnonymousID.Set(req.AnonymousId),
		db.ConversionEvent.EventName.Set(req.EventName),
		db.ConversionEvent.EventValue.Set(req.EventValue),
		db.ConversionEvent.Properties.Set(propertiesJson),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to track conversion"})
		return
	}

	// Mark assignment as converted if this is the first conversion
	if !assignment.Converted {
		now := time.Now()
		_, err = s.client.ExperimentAssignment.FindUnique(
			db.ExperimentAssignment.ID.Equals(assignment.ID),
		).Update(
			db.ExperimentAssignment.Converted.Set(true),
			db.ExperimentAssignment.ConvertedAt.Set(now),
		).Exec(c.Request.Context())

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update assignment"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (s *Server) GetExperimentResults(c *gin.Context) {
	experimentId := c.Param("id")

	// Get assignments grouped by variant
	assignments, err := s.client.ExperimentAssignment.FindMany(
		db.ExperimentAssignment.Experiment.Where(db.Experiment.ID.Equals(experimentId)),
	).With(
		db.ExperimentAssignment.Variant.Fetch(),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch assignments"})
		return
	}

	// Get conversion events
	conversions, err := s.client.ConversionEvent.FindMany(
		db.ConversionEvent.ExperimentID.Equals(experimentId),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch conversions"})
		return
	}

	// Calculate results
	results := make(map[string]interface{})
	variantStats := make(map[string]map[string]interface{})

	// Initialize variant stats
	for _, assignment := range assignments {
		variantId := assignment.VariantID
		variant := assignment.Variant()
		if _, exists := variantStats[variantId]; !exists {
			variantStats[variantId] = map[string]interface{}{
				"variantId":      variantId,
				"variantName":    variant.Name,
				"variantKey":     variant.Key,
				"isControl":      variant.IsControl,
				"assignments":    0,
				"conversions":    0,
				"conversionRate": 0.0,
				"totalValue":     0.0,
			}
		}
		variantStats[variantId]["assignments"] = variantStats[variantId]["assignments"].(int) + 1
	}

	// Count conversions
	for _, conversion := range conversions {
		variantId := conversion.VariantID
		if stats, exists := variantStats[variantId]; exists {
			stats["conversions"] = stats["conversions"].(int) + 1
			stats["totalValue"] = stats["totalValue"].(float64) + conversion.EventValue
		}
	}

	// Calculate conversion rates
	for _, stats := range variantStats {
		assignments := stats["assignments"].(int)
		conversions := stats["conversions"].(int)
		if assignments > 0 {
			stats["conversionRate"] = float64(conversions) / float64(assignments)
		}
	}

	results["variants"] = variantStats
	results["totalAssignments"] = len(assignments)
	results["totalConversions"] = len(conversions)

	c.JSON(http.StatusOK, results)
}

// Helper function to select variant based on traffic split
func (s *Server) selectVariant(variants []db.VariantModel, seed string) db.VariantModel {
	// Create deterministic random based on seed
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	hash := md5.Sum([]byte(seed))
	r.Seed(int64(hash[0])<<24 | int64(hash[1])<<16 | int64(hash[2])<<8 | int64(hash[3]))

	random := r.Float64()
	cumulative := 0.0

	for _, variant := range variants {
		cumulative += variant.TrafficSplit
		if random <= cumulative {
			return variant
		}
	}

	// Fallback to last variant
	return variants[len(variants)-1]
}

func (s *Server) UpdateExperimentStatus(c *gin.Context) {
	experimentId := c.Param("id")
	newStatus := c.PostForm("status")

	// Validate status
	validStatuses := map[string]bool{
		"DRAFT":     true,
		"RUNNING":   true,
		"PAUSED":    true,
		"COMPLETED": true,
		"ARCHIVED":  true,
	}

	if !validStatuses[newStatus] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
		return
	}

	// Update experiment status
	_, err := s.client.Experiment.FindUnique(
		db.Experiment.ID.Equals(experimentId),
	).Update(
		db.Experiment.Status.Set(db.ExperimentStatus(newStatus)),
	).Exec(c.Request.Context())

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update experiment status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
