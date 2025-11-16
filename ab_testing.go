package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type CreateExperimentRequest struct {
	Name              string             `json:"name" binding:"required"`
	Description       string             `json:"description"`
	TrafficAllocation float64            `json:"trafficAllocation" binding:"required"`
	ProjectID         string             `json:"projectId"`
	Variants          []CreateVariantReq `json:"variants" binding:"required,min=1"`
	Goals             []CreateGoalReq    `json:"goals"`

	// Optional fields
	Key       *string    `json:"key"`
	Status    string     `json:"status"`
	StartDate *time.Time `json:"startDate"`
	EndDate   *time.Time `json:"endDate"`
}

type CreateVariantReq struct {
	Name          string                `json:"name" binding:"required"`
	Description   string                `json:"description"`
	TrafficWeight float64               `json:"trafficWeight" binding:"required"`
	IsControl     bool                  `json:"isControl"`
	Changes       []CreateVariantChange `json:"changes"`

	// Optional backend fields
	Key *string `json:"key"`
}

type CreateVariantChange struct {
	Selector string `json:"selector"`
	Property string `json:"property"`
	Value    string `json:"value"`
	Type     string `json:"type"`
}

type CreateGoalReq struct {
	Name      string `json:"name" binding:"required"`
	Type      string `json:"type" binding:"required"`
	Target    string `json:"target" binding:"required"`
	IsPrimary bool   `json:"isPrimary"`
}

type GetExperimentRequest struct {
	ExperimentKey string `form:"experimentKey" binding:"required"`
	ProjectID     string `form:"projectId" binding:"required"`
}
type GetAssignmentRequest struct {
	ExperimentKey string `form:"experimentKey" binding:"required"`
	ProjectID     string `form:"projectId" binding:"required"`
	UserID        string `form:"userId"`
	AnonymousID   string `form:"anonymousId"`
}

type TrackConversionRequest struct {
	ExperimentID string                 `json:"experimentId" binding:"required"`
	EventName    string                 `json:"eventName" binding:"required"`
	Value        *float64               `json:"value"`
	UserID       string                 `json:"userId"`
	AnonymousID  string                 `json:"anonymousId"`
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
	projectID := c.Param("id")

	var req CreateExperimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set project ID from route parameter
	req.ProjectID = projectID

	// Generate key if not provided
	experimentKey := fmt.Sprintf("exp_%s", uuid.New().String()[:8])
	if req.Key != nil && *req.Key != "" {
		experimentKey = *req.Key
	}

	// Set default status if not provided
	status := "DRAFT"
	if req.Status != "" {
		status = req.Status
	}

	// Convert description to pointer
	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	// Create experiment and variants within a transaction
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Create the Experiment
		experiment := Experiment{
			ID:           uuid.New().String(),
			Name:         req.Name,
			Key:          experimentKey,
			Status:       status,
			TrafficSplit: req.TrafficAllocation,
			ProjectID:    req.ProjectID,
			Description:  description,
			StartDate:    req.StartDate,
			EndDate:      req.EndDate,
		}

		if err := tx.Create(&experiment).Error; err != nil {
			return fmt.Errorf("failed to create experiment: %w", err)
		}

		// Create the Variants and link them to the Experiment
		for _, v := range req.Variants {
			variantKey := fmt.Sprintf("var_%s", uuid.New().String()[:8])
			if v.Key != nil && *v.Key != "" {
				variantKey = *v.Key
			}

			var variantDesc *string
			if v.Description != "" {
				variantDesc = &v.Description
			}

			variant := Variant{
				ID:           uuid.New().String(),
				Name:         v.Name,
				Key:          variantKey,
				Description:  variantDesc,
				IsControl:    v.IsControl,
				TrafficSplit: v.TrafficWeight,
				ExperimentID: experiment.ID,
			}

			if err := tx.Create(&variant).Error; err != nil {
				return fmt.Errorf("failed to create variant %s: %w", v.Name, err)
			}
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Fetch the created experiment with its variants
	var experiment Experiment
	if err := s.db.Preload("Variants").Where("project_id = ?", req.ProjectID).Order("created_at DESC").First(&experiment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch created experiment"})
		return
	}

	c.JSON(http.StatusOK, experiment)
}

func (s *Server) GetExperiments(c *gin.Context) {
	projectID := c.Param("id")

	var experiments []Experiment
	if err := s.db.Preload("Variants").Where("project_id = ?", projectID).Find(&experiments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch experiments"})
		return
	}

	var responses []ExperimentResponse
	for _, exp := range experiments {
		var variants []VariantResponse
		for _, variant := range exp.Variants {
			description := ""
			if variant.Description != nil {
				description = *variant.Description
			}
			variants = append(variants, VariantResponse{
				Id:           variant.ID,
				Name:         variant.Name,
				Key:          variant.Key,
				Description:  description,
				IsControl:    variant.IsControl,
				TrafficSplit: variant.TrafficSplit,
				CreatedAt:    variant.CreatedAt,
				UpdatedAt:    variant.UpdatedAt,
			})
		}

		description := ""
		if exp.Description != nil {
			description = *exp.Description
		}

		responses = append(responses, ExperimentResponse{
			Id:           exp.ID,
			Name:         exp.Name,
			Description:  description,
			Key:          exp.Key,
			Status:       exp.Status,
			TrafficSplit: exp.TrafficSplit,
			StartDate:    exp.StartDate,
			EndDate:      exp.EndDate,
			CreatedAt:    exp.CreatedAt,
			UpdatedAt:    exp.UpdatedAt,
			Variants:     variants,
		})
	}

	c.JSON(http.StatusOK, responses)
}

func (s *Server) GetExperiment(c *gin.Context) {
	experimentID := c.Param("experimentId")
	projectID := c.Param("id")

	var experiment Experiment
	if err := s.db.Preload("Variants").
		Where("id = ? AND project_id = ?", experimentID, projectID).
		First(&experiment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get experiment"})
		return
	}

	var variants []VariantResponse
	for _, variant := range experiment.Variants {
		description := ""
		if variant.Description != nil {
			description = *variant.Description
		}
		variants = append(variants, VariantResponse{
			Id:           variant.ID,
			Name:         variant.Name,
			Key:          variant.Key,
			Description:  description,
			IsControl:    variant.IsControl,
			TrafficSplit: variant.TrafficSplit,
			CreatedAt:    variant.CreatedAt,
			UpdatedAt:    variant.UpdatedAt,
		})
	}

	description := ""
	if experiment.Description != nil {
		description = *experiment.Description
	}

	response := ExperimentResponse{
		Id:           experiment.ID,
		Name:         experiment.Name,
		Description:  description,
		Key:          experiment.Key,
		Status:       experiment.Status,
		TrafficSplit: experiment.TrafficSplit,
		StartDate:    experiment.StartDate,
		EndDate:      experiment.EndDate,
		CreatedAt:    experiment.CreatedAt,
		UpdatedAt:    experiment.UpdatedAt,
		Variants:     variants,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) GetAssignment(c *gin.Context) {
	var req GetAssignmentRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.UserID == "" && req.AnonymousID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id or anonymous_id is required"})
		return
	}

	// Fetch experiment and variants
	var experiment Experiment
	if err := s.db.Preload("Variants").
		Where("key = ? AND project_id = ? AND status = ?", req.ExperimentKey, req.ProjectID, "RUNNING").
		First(&experiment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get experiment"})
		return
	}

	// Check if user is already assigned
	var assignment ExperimentAssignment
	query := s.db.Where("experiment_id = ?", experiment.ID)
	if req.UserID != "" {
		query = query.Where("user_id = ?", req.UserID)
	} else {
		query = query.Where("anonymous_id = ?", req.AnonymousID)
	}

	err := query.First(&assignment).Error
	if err == nil {
		// Found existing assignment
		var variant Variant
		if err := s.db.Where("id = ?", assignment.VariantID).First(&variant).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get assigned variant"})
			return
		}
		c.JSON(http.StatusOK, variant)
		return
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for existing assignment"})
		return
	}

	// Assign user to a variant
	selectedVariant := assignVariant(req.UserID, req.AnonymousID, experiment.Variants)

	// Create assignment
	newAssignment := ExperimentAssignment{
		ID:           uuid.New().String(),
		ExperimentID: experiment.ID,
		VariantID:    selectedVariant.ID,
	}

	if req.UserID != "" {
		newAssignment.UserID = &req.UserID
	}
	if req.AnonymousID != "" {
		newAssignment.AnonymousID = &req.AnonymousID
	}

	if err := s.db.Create(&newAssignment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create assignment"})
		return
	}

	c.JSON(http.StatusOK, selectedVariant)
}

func (s *Server) TrackConversion(c *gin.Context) {
	var req TrackConversionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.UserID == "" && req.AnonymousID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id or anonymous_id is required"})
		return
	}

	// Find assignment
	var assignment ExperimentAssignment
	query := s.db.Where("experiment_id = ?", req.ExperimentID)
	if req.UserID != "" {
		query = query.Where("user_id = ?", req.UserID)
	} else {
		query = query.Where("anonymous_id = ?", req.AnonymousID)
	}

	if err := query.First(&assignment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Assignment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find assignment"})
		return
	}

	// Marshal properties to JSON
	var propertiesJSON []byte
	var err error
	if req.Properties != nil {
		propertiesJSON, err = json.Marshal(req.Properties)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid properties format"})
			return
		}
	}

	// Create conversion event
	conversionEvent := ConversionEvent{
		ID:           uuid.New().String(),
		EventName:    req.EventName,
		EventValue:   req.Value,
		ExperimentID: req.ExperimentID,
		VariantID:    assignment.VariantID,
		Properties:   propertiesJSON,
	}

	if req.UserID != "" {
		conversionEvent.UserID = &req.UserID
	}
	if req.AnonymousID != "" {
		conversionEvent.AnonymousID = &req.AnonymousID
	}

	if err := s.db.Create(&conversionEvent).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to track conversion"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) GetExperimentResults(c *gin.Context) {
	experimentID := c.Param("experimentId")
	projectID := c.Param("id")

	// Verify experiment belongs to project
	var experiment Experiment
	if err := s.db.Where("id = ? AND project_id = ?", experimentID, projectID).First(&experiment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find experiment"})
		return
	}

	// Fetch conversions
	var conversions []ConversionEvent
	if err := s.db.Where("experiment_id = ?", experimentID).Find(&conversions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get conversions"})
		return
	}

	// Calculate results
	stats := make(map[string]map[string]interface{})
	for _, conversion := range conversions {
		variantID := conversion.VariantID
		if _, ok := stats[variantID]; !ok {
			stats[variantID] = map[string]interface{}{
				"totalConversions": 0,
				"totalValue":       0.0,
				"uniqueUsers":      make(map[string]bool),
			}
		}
		stats[variantID]["totalConversions"] = stats[variantID]["totalConversions"].(int) + 1
		if conversion.EventValue != nil {
			stats[variantID]["totalValue"] = stats[variantID]["totalValue"].(float64) + *conversion.EventValue
		}
		if conversion.UserID != nil {
			stats[variantID]["uniqueUsers"].(map[string]bool)[*conversion.UserID] = true
		} else if conversion.AnonymousID != nil {
			stats[variantID]["uniqueUsers"].(map[string]bool)[*conversion.AnonymousID] = true
		}
	}

	// Format results
	var results []gin.H
	for variantID, data := range stats {
		results = append(results, gin.H{
			"variantId":        variantID,
			"totalConversions": data["totalConversions"],
			"totalValue":       data["totalValue"],
			"uniqueUsers":      len(data["uniqueUsers"].(map[string]bool)),
		})
	}

	c.JSON(http.StatusOK, results)
}

func (s *Server) UpdateExperiment(c *gin.Context) {
	experimentID := c.Param("experimentId")
	projectID := c.Param("id")

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify experiment belongs to project
	var experiment Experiment
	if err := s.db.Where("id = ? AND project_id = ?", experimentID, projectID).First(&experiment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find experiment"})
		return
	}

	// Update experiment
	if err := s.db.Model(&experiment).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update experiment"})
		return
	}

	// Fetch updated experiment with variants
	if err := s.db.Preload("Variants").Where("id = ?", experimentID).First(&experiment).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch updated experiment"})
		return
	}

	c.JSON(http.StatusOK, experiment)
}

func (s *Server) DeleteExperiment(c *gin.Context) {
	experimentID := c.Param("experimentId")
	projectID := c.Param("id")

	// Verify experiment belongs to project and delete
	result := s.db.Where("id = ? AND project_id = ?", experimentID, projectID).Delete(&Experiment{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete experiment"})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Experiment not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (s *Server) UpdateExperimentStatus(c *gin.Context) {
	experimentID := c.Param("id")
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
	if err := s.db.Model(&Experiment{}).Where("id = ?", experimentID).Update("status", newStatus).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update experiment status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func assignVariant(userID, anonymousID string, variants []Variant) Variant {
	if len(variants) == 0 {
		return Variant{}
	}

	identifier := userID
	if identifier == "" {
		identifier = anonymousID
	}

	// Simple hash-based assignment
	hash := sha1.New()
	hash.Write([]byte(identifier))
	hashBytes := hash.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

	// Use the first 8 characters of the hash as a number
	hashValue := int64(0)
	for i := 0; i < 8 && i < len(hashString); i++ {
		hashValue = hashValue*16 + int64(hexCharToInt(rune(hashString[i])))
	}

	// Calculate cumulative traffic split
	totalSplit := 0.0
	for _, v := range variants {
		totalSplit += v.TrafficSplit
	}

	// Normalize hash value to 0-1 range
	roll := float64(hashValue%10000) / 10000.0
	if totalSplit > 0 {
		roll = roll * totalSplit
	}

	// Select variant based on roll
	cumulativeSplit := 0.0
	for _, v := range variants {
		cumulativeSplit += v.TrafficSplit
		if roll < cumulativeSplit {
			return v
		}
	}

	// Fallback to last variant
	return variants[len(variants)-1]
}

func hexCharToInt(c rune) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	if c >= 'a' && c <= 'f' {
		return int(c - 'a' + 10)
	}
	if c >= 'A' && c <= 'F' {
		return int(c - 'A' + 10)
	}
	return 0
}
