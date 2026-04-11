package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// GetOrCreateProjectSettings returns the settings for a project, creating defaults if none exist
func GetOrCreateProjectSettings(db *gorm.DB, projectID string) (*ProjectSettings, error) {
	var settings ProjectSettings
	err := db.Where("project_id = ?", projectID).First(&settings).Error
	if err == nil {
		return &settings, nil
	}

	settings = ProjectSettings{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&settings).Error; err != nil {
		// Might have been created concurrently
		if err2 := db.Where("project_id = ?", projectID).First(&settings).Error; err2 != nil {
			return nil, err
		}
	}
	return &settings, nil
}

// GetProjectSettingsHandler returns project settings
func GetProjectSettingsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := c.Param("project_id")

		settings, err := GetOrCreateProjectSettings(db, projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"settings": settings})
	}
}

// UpdateProjectSettingsRequest is the request body for updating project settings
type UpdateProjectSettingsRequest struct {
	MaxEmailCharacters *int `json:"max_email_characters"`
}

// UpdateProjectSettingsHandler updates project settings
func UpdateProjectSettingsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := c.Param("project_id")

		var req UpdateProjectSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		settings, err := GetOrCreateProjectSettings(db, projectID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load settings"})
			return
		}

		updates := map[string]interface{}{
			"updated_at": time.Now(),
		}

		if req.MaxEmailCharacters != nil {
			val := *req.MaxEmailCharacters
			if val < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "max_email_characters must be non-negative (0 = no limit)"})
				return
			}
			updates["max_email_characters"] = val
		}

		if err := db.Model(settings).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update settings"})
			return
		}

		// Re-read
		db.Where("project_id = ?", projectID).First(settings)
		c.JSON(http.StatusOK, gin.H{"settings": settings})
	}
}
