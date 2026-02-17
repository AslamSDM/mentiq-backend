package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mileusna/useragent"
)

// ingestEventHandler handles single event ingestion - saves directly to TimescaleDB
func (as *AnalyticsService) ingestEventHandler(c *gin.Context) {
	var event Event
	if err := c.ShouldBindJSON(&event); err != nil {
		log.Printf("Failed to decode event: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	// Extract account and project from context (set by middleware)
	accountID, accountExists := c.Get("account_id")
	projectID, projectExists := c.Get("project_id")

	// Set account ID
	if accountExists && accountID != nil {
		if accID, ok := accountID.(string); ok && accID != "" {
			event.AccountID = accID
		}
	}

	// Set project ID (prefer from event payload, fall back to context)
	if event.ProjectID == "" {
		if projectExists && projectID != nil {
			if projID, ok := projectID.(string); ok && projID != "" {
				event.ProjectID = projID
			}
		}
	}

	// Validate required fields
	if event.AccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id is required"})
		return
	}
	if event.ProjectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id is required (set X-Project-ID header or include in payload)"})
		return
	}

	// Generate event ID if not provided or invalid UUID
	if event.EventID == "" {
		event.EventID = uuid.New().String()
	} else {
		// Validate if it's a valid UUID, if not generate a new one
		if _, err := uuid.Parse(event.EventID); err != nil {
			log.Printf("Invalid UUID '%s', generating new one", event.EventID)
			event.EventID = uuid.New().String()
		}
	}

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Extract client info
	event.UserAgent = c.GetHeader("User-Agent")
	event.IPAddress = getClientIPFromGin(c)

	// Parse User-Agent
	ua := useragent.Parse(event.UserAgent)
	event.Browser = ua.Name
	event.OS = ua.OS
	event.Device = ua.Device

	// Get Geo Location
	country, city := getGeoLocation(event.IPAddress)
	event.Country = country
	event.City = city

	// Extract channel and email from properties
	if event.Properties != nil {
		if channel, ok := event.Properties["channel"].(string); ok && channel != "" {
			event.Channel = channel
		}
		if email, ok := event.Properties["email"].(string); ok && email != "" {
			event.Email = email
		}
	}

	// Validate required fields
	if event.EventType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_type is required"})
		return
	}

	// Enqueue event for async batch insert
	if !as.eventQueue.Enqueue(event) {
		log.Printf("Event queue full, dropping event %s", event.EventID)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "Event queue is full, try again shortly",
			"retryable": true,
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"status":   "accepted",
		"event_id": event.EventID,
		"queued":   true,
	})
}

// ingestBatchEventsHandler handles batch event ingestion - saves all events to TimescaleDB in one transaction
// batchIngestHandler handles batch event ingestion - saves all events to TimescaleDB in one transaction
func (as *AnalyticsService) batchIngestHandler(c *gin.Context) {
	var events []Event
	if err := c.ShouldBindJSON(&events); err != nil {
		log.Printf("Failed to decode batch events: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON payload"})
		return
	}

	if len(events) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No events in batch"})
		return
	}

	// Extract account and project from context
	accountID, accountExists := c.Get("account_id")
	projectID, projectExists := c.Get("project_id")

	// Get account/project IDs safely
	var accountIDStr, projectIDStr string
	if accountExists && accountID != nil {
		if accID, ok := accountID.(string); ok {
			accountIDStr = accID
		}
	}
	if projectExists && projectID != nil {
		if projID, ok := projectID.(string); ok {
			projectIDStr = projID
		}
	}

	// Process each event
	for i := range events {
		event := &events[i]

		// Set account/project from context if not in event payload
		if event.AccountID == "" {
			event.AccountID = accountIDStr
		}
		if event.ProjectID == "" {
			event.ProjectID = projectIDStr
		}

		// Validate required fields
		if event.AccountID == "" || event.ProjectID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Event %d missing account_id or project_id", i),
			})
			return
		}

		// Generate event ID if not provided or invalid UUID
		if event.EventID == "" {
			event.EventID = uuid.New().String()
		} else {
			// Validate if it's a valid UUID, if not generate a new one
			if _, err := uuid.Parse(event.EventID); err != nil {
				log.Printf("Invalid UUID '%s', generating new one", event.EventID)
				event.EventID = uuid.New().String()
			}
		}

		// Set timestamp if not provided
		if event.Timestamp.IsZero() {
			event.Timestamp = time.Now().UTC()
		}

		// Extract client info
		if event.UserAgent == "" {
			event.UserAgent = c.GetHeader("User-Agent")
		}
		if event.IPAddress == "" {
			event.IPAddress = getClientIPFromGin(c)
		}

		// Parse User-Agent
		if event.Browser == "" || event.OS == "" || event.Device == "" {
			ua := useragent.Parse(event.UserAgent)
			if event.Browser == "" {
				event.Browser = ua.Name
			}
			if event.OS == "" {
				event.OS = ua.OS
			}
			if event.Device == "" {
				event.Device = ua.Device
			}
		}

		// Get Geo Location
		if event.Country == "" || event.City == "" {
			country, city := getGeoLocation(event.IPAddress)
			if event.Country == "" {
				event.Country = country
			}
			if event.City == "" {
				event.City = city
			}
		}

		// Extract channel and email from properties
		if event.Properties != nil {
			if channel, ok := event.Properties["channel"].(string); ok && channel != "" && event.Channel == "" {
				event.Channel = channel
			}
			if email, ok := event.Properties["email"].(string); ok && email != "" && event.Email == "" {
				event.Email = email
			}
		}
	}

	// Enqueue all events for async batch insert
	enqueued := as.eventQueue.EnqueueBatch(events)

	if enqueued == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "Event queue is full, try again shortly",
			"retryable": true,
		})
		return
	}

	status := http.StatusAccepted
	message := fmt.Sprintf("%d events queued for processing", enqueued)
	if enqueued < len(events) {
		message = fmt.Sprintf("%d of %d events queued (queue near capacity)", enqueued, len(events))
	}

	c.JSON(status, gin.H{
		"status":           "accepted",
		"events_queued":    enqueued,
		"events_submitted": len(events),
		"message":          message,
	})
}

// flushCacheHandler manually triggers cache flush (legacy endpoint - now a no-op with TimescaleDB)
func (as *AnalyticsService) flushCacheHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "No cache to flush - events are saved directly to TimescaleDB",
	})
}
