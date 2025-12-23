# Analytics Platform Makefile

.PHONY: build run test clean docker-build docker-run install deps dev install-air

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=analytics-platform
BINARY_UNIX=$(BINARY_NAME)_unix

# Build the application
build:
	$(GOBUILD) -o $(BINARY_NAME) -v ./main.go

# Install dependencies
deps:
	$(GOCMD) mod download
	$(GOCMD) mod tidy

# Run the application
run:
	$(GOBUILD) -o $(BINARY_NAME) -v ./main.go
	./$(BINARY_NAME)

# Run with environment variables loaded
run-env:
	@if [ -f .env ]; then \
		export $$(cat .env | xargs) && ./$(BINARY_NAME); \
	else \
		echo "No .env file found. Please create one from .env.example"; \
	fi

# Run tests
test:
	$(GOTEST) -v ./...

# Clean build artifacts
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

# Test the API endpoints
test-api:
	./test.sh

# Build for Linux (useful for deployment)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v ./main.go

# Docker build
docker-build:
	docker build -t analytics-platform .

# Docker run
docker-run:
	docker run -p 8080:8080 --env-file .env analytics-platform

# Docker compose up
docker-up:
	docker-compose up -d

# Docker compose down
docker-down:
	docker-compose down

# Setup environment
setup:
	@echo "Setting up Analytics Platform..."
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "Created .env file from .env.example"; \
		echo "Please edit .env with your configuration"; \
	else \
		echo ".env file already exists"; \
	fi
	$(GOCMD) mod download
	$(GOCMD) mod tidy
	@echo "Setup complete!"

# Install Air for hot reloading
install-air:
	@echo "Installing Air for hot reloading..."
	go install github.com/air-verse/air@latest
	@echo "Air installed! Make sure $(GOPATH)/bin is in your PATH"

# Run with hot reloading (development mode)
dev:
	@if command -v air >/dev/null 2>&1; then \
		air; \
	else \
		echo "Air is not installed. Run 'make install-air' first."; \
		exit 1; \
	fi

# Show help
help:
	@echo "Available commands:"
	@echo "  build       - Build the application"
	@echo "  run         - Build and run the application"
	@echo "  run-env     - Run with environment variables from .env file"
	@echo "  dev         - Run with hot reloading (requires Air)"
	@echo "  install-air - Install Air for hot reloading"
	@echo "  test        - Run Go tests"
	@echo "  test-api    - Test API endpoints with curl"
	@echo "  clean       - Clean build artifacts"
	@echo "  deps        - Download and tidy dependencies"
	@echo "  setup       - Initial project setup"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run  - Run Docker container"
	@echo "  docker-up   - Start with docker-compose"
	@echo "  docker-down - Stop docker-compose"
	@echo "  help        - Show this help message"

# Default target
all: deps build

