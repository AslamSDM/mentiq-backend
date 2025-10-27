# Mentiq Analytics Backend API Documentation

## Overview

The Mentiq Analytics Backend is a high-performance analytics and event ingestion system built with Go, designed to handle high-throughput data collection and provide real-time analytics insights. The system features multi-layer caching (Redis + in-memory), batch processing, and comprehensive user analytics including DAU, WAU, and MAU metrics.

## Base URL

```
http://localhost:8080/api/v1
```

## Authentication

All API endpoints require authentication using an API key and a Project ID. Include your credentials in the request headers:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

### Authentication Flow

1.  **Create Account**: Sign up to get an account.
2.  **Create Project**: Create a project within your account.
3.  **Generate API Key**: Generate an API key for your project.
4.  **Use API Key and Project ID**: Include the API key and Project ID in all subsequent requests.

---

## Core Event Ingestion Endpoints

### 1. Ingest Single Event

**Endpoint**: `POST /api/v1/events`

**Description**: Ingest a single analytics event into the system. Events are queued for batch processing to ensure high performance.

**Headers**:

```
Content-Type: application/json
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

**Request Body**:

```json
{
  "event_type": "page_view",
  "user_id": "user_12345",
  "session_id": "session_abc123",
  "timestamp": "2024-01-15T10:30:00Z",
  "properties": {
    "page": "/dashboard",
    "referrer": "https://google.com",
    "utm_source": "newsletter",
    "browser": "Chrome",
    "device_type": "desktop"
  }
}
```

**Response**:

```json
{
  "status": "success",
  "event_id": "550e8400-e29b-41d4-a716-446655440000",
  "message": "Event queued for processing",
  "cache_size": 142
}
```

---

### 2. Batch Event Ingestion

**Endpoint**: `POST /api/v1/events/batch`

**Description**: Ingest multiple events in a single request for improved performance. Recommended for high-volume applications.

**Headers**:

```
Content-Type: application/json
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

**Request Body**:

```json
[
  {
    "event_type": "page_view",
    "user_id": "user_12345",
    "session_id": "session_abc123",
    "timestamp": "2024-01-15T10:30:00Z",
    "properties": {
      "page": "/dashboard"
    }
  },
  {
    "event_type": "click",
    "user_id": "user_12345",
    "session_id": "session_abc123",
    "timestamp": "2024-01-15T10:31:00Z",
    "properties": {
      "element": "signup_button",
      "page": "/landing"
    }
  }
]
```

**Response**:

```json
{
  "status": "success",
  "events_processed": 2,
  "message": "Batch events queued for processing",
  "cache_size": 144
}
```

---

## Analytics & Metrics Endpoints

### 3. Get Analytics Data

**Endpoint**: `GET /api/v1/analytics`

**Description**: Retrieve comprehensive analytics data including event counts, unique users, and time-series data.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

### 4. Get User Metrics (DAU/WAU/MAU)

**Endpoint**: `GET /api/v1/user-metrics`

**Description**: Get Daily Active Users (DAU), Weekly Active Users (WAU), and Monthly Active Users (MAU) metrics.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

### 5. Analytics Dashboard Data

**Endpoint**: `GET /api/v1/dashboard`

**Description**: Get summarized dashboard data for a specific date, optimized for dashboard displays.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

### 6. Get Heatmap Analytics

**Endpoint**: `GET /api/v1/analytics/heatmaps`

**Description**: Retrieve heatmap data for a specific page.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

**Query Parameters**:

- `startDate`: YYYY-MM-DD
- `endDate`: YYYY-MM-DD
- `path`: The path of the page to retrieve heatmap data for.

### 7. Get Error Analytics

**Endpoint**: `GET /api/v1/analytics/errors`

**Description**: Retrieve aggregated error analytics.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

### 8. Get Session Analytics

**Endpoint**: `GET /api/v1/sessions/:session_id`

**Description**: Retrieve detailed analytics for a specific session.

**Headers**:

```
Authorization: ApiKey YOUR_API_KEY
X-Project-ID: YOUR_PROJECT_ID
```

---

## Project & Account Management

### 9. Create Project

**Endpoint**: `POST /api/v1/projects`

**Description**: Create a new project within your account.

**Headers**:

```
Content-Type: application/json
Authorization: ApiKey YOUR_ACCOUNT_TOKEN
```

### 10. Generate API Key

**Endpoint**: `POST /api/v1/projects/{project_id}/apikeys`

**Description**: Generate a new API key for a specific project.

**Headers**:

```
Content-Type: application/json
Authorization: ApiKey YOUR_ACCOUNT_TOKEN
```

### 11. List Projects

**Endpoint**: `GET /api/v1/projects`

**Description**: List all projects in your account.

**Headers**:

```
Authorization: ApiKey YOUR_ACCOUNT_TOKEN
```

---

## Authentication Endpoints

### 12. User Signup

**Endpoint**: `POST /signup`

### 13. User Login

**Endpoint**: `POST /login`

---

## Utility Endpoints

### 14. Health Check

**Endpoint**: `GET /health`

### 15. Flush Cache

**Endpoint**: `POST /api/v1/flush-cache`
