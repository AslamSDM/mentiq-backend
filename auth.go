package main

import (
	"context"
	"net/http"
	"strings"

	"mentiq-backend/prisma/db"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware validates the API key and project ID.
func AuthMiddleware(dbClient *db.PrismaClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		// In a real app, you'd validate a JWT or session token.
		// For now, we'll use a static API key from the Authorization header
		// and a project ID from the X-Project-ID header.
		authHeader := c.GetHeader("Authorization")
		projectID := c.GetHeader("X-Project-ID")

		if authHeader == "" || projectID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header and X-Project-ID header are required"})
			return
		}

		apiKey := authHeader
		if strings.HasPrefix(apiKey, "Bearer ") {
			apiKey = strings.TrimPrefix(apiKey, "Bearer ")
		} else if strings.HasPrefix(apiKey, "ApiKey ") {
			apiKey = strings.TrimPrefix(apiKey, "ApiKey ")
		}

		// The API key is treated as the account ID for this example.
		account, err := dbClient.Account.FindFirst(
			db.Account.ID.Equals(apiKey),
		).Exec(context.Background())

		if err != nil {
			if err == db.ErrNotFound {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Could not verify account"})
			return
		}

		// Verify the project belongs to the account.
		project, err := dbClient.Project.FindFirst(
			db.Project.ID.Equals(projectID),
			db.Project.AccountID.Equals(account.ID),
		).Exec(context.Background())

		if err != nil {
			if err == db.ErrNotFound {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Invalid Project ID for this account"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Could not verify project"})
			return
		}

		// Set account and project info in the context for handlers to use.
		c.Set("account_id", account.ID)
		c.Set("project_id", project.ID)

		c.Next()
	}
}

type SignupRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

func signupHandler(dbClient *db.PrismaClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SignupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// In a real app, you would hash the password
		// For this example, we'll store it as-is.
		account, err := dbClient.Account.CreateOne(
			db.Account.Name.Set(req.Name),
			db.Account.Email.Set(req.Email),
			db.Account.Password.Set(req.Password), // Storing plaintext password - NOT FOR PRODUCTION
		).Exec(context.Background())

		if err != nil {
			// Basic check for duplicate email
			if strings.Contains(err.Error(), "UniqueConstraintViolation") {
				c.JSON(http.StatusConflict, gin.H{"error": "Email already in use"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"message": "Account created successfully",
			"account": gin.H{
				"id":    account.ID,
				"name":  account.Name,
				"email": account.Email,
			},
		})
	}
}
