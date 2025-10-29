# Mentiq Analytics API Documentation

Complete API reference for the Mentiq Analytics Platform. This documentation covers all endpoints except event ingestion (which is covered separately in `EVENT_INGESTION.md`).

## Table of Contents

1. [Authentication](#authentication)
2. [Public Endpoints](#public-endpoints)
3. [Authentication Endpoints](#authentication-endpoints)
4. [Project Management](#project-management)
5. [API Key Management](#api-key-management)
6. [Analytics Endpoints](#analytics-endpoints)
7. [A/B Testing Endpoints](#ab-testing-endpoints)
8. [Error Handling](#error-handling)

---

## Authentication

All protected endpoints (except `/health`, `/signup`, `/login`) require authentication via the `Authorization` header.

### Authentication Methods

#### 1. JWT Token (Recommended)
Obtained from the `/login` endpoint. Valid for 24 hours.

```bash
curl -X GET http://localhost:8080/api/v1/projects \
  -H "Authorization: Bearer <JWT_TOKEN>"
```

**Token Format:** `accountID.email.expiryUnixTimestamp`

#### 2. API Key
Generated from the `/api/v1/projects/:project_id/apikeys` endpoint. Use for event ingestion.

```bash
curl -X POST http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer <API_KEY>"
```

### Required Headers

| Header | Description | Required |
|--------|-------------|----------|
| `Authorization` | Bearer token (JWT or API Key) | Yes (all protected routes) |
| `X-Project-ID` | Project ID (for multi-project contexts) | For analytics endpoints |
| `Content-Type` | `application/json` | For POST/PUT requests |

---

## Public Endpoints

### Health Check
Check if the server is running.

```
GET /health
```

**Response:**
```json
{
  "status": "healthy",
  "timestamp": "2024-01-15T10:30:00Z",
  "service": "analytics-platform"
}
```

**Status Code:** `200 OK`

---

## Authentication Endpoints

### Sign Up
Create a new user account.

```
POST /signup
Content-Type: application/json
```

**Request Body:**
```json
{
  "name": "John Doe",
  "email": "john@example.com",
  "password": "securepassword123"
}
```

**Validation Rules:**
- `name`: Required, non-empty string
- `email`: Required, valid email format
- `password`: Required, minimum 8 characters

**Response (201 Created):**
```json
{
  "message": "Account created successfully",
  "account": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "John Doe",
    "email": "john@example.com"
  }
}
```

**Error Response (409 Conflict):**
```json
{
  "error": "Email already in use"
}
```

**Error Response (400 Bad Request):**
```json
{
  "error": "Invalid query parameters"
}
```

---

### Login
Authenticate and obtain a JWT token.

```
POST /login
Content-Type: application/json
```

**Request Body:**
```json
{
  "email": "john@example.com",
  "password": "securepassword123"
}
```

**Response (200 OK):**
```json
{
  "token": "550e8400-e29b-41d4-a716-446655440000.john@example.com.1730000000",
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "John Doe",
    "email": "john@example.com"
  }
}
```

**Token Expiration:** 24 hours

**Error Response (401 Unauthorized):**
```json
{
  "error": "Invalid credentials"
}
```

---

## Project Management

All project endpoints require JWT authentication.

### Create Project
Create a new analytics project.

```
POST /api/v1/projects
Authorization: Bearer <JWT_TOKEN>
Content-Type: application/json
```

**Request Body:**
```json
{
  "name": "My Analytics Project"
}
```

**Response (201 Created):**
```json
{
  "id": "proj_550e8400e29b41d4",
  "name": "My Analytics Project",
  "accountId": "550e8400-e29b-41d4-a716-446655440000",
  "createdAt": "2024-01-15T10:30:00Z",
  "updatedAt": "2024-01-15T10:30:00Z"
}
```

**Error Response (400 Bad Request):**
```json
{
  "error": "name is required"
}
```

---

### List Projects
Get all projects for the current account.

```
GET /api/v1/projects
Authorization: Bearer <JWT_TOKEN>
```

**Response (200 OK):**
```json
[
  {
    "id": "proj_550e8400e29b41d4",
    "name": "My Analytics Project",
    "accountId": "550e8400-e29b-41d4-a716-446655440000",
    "createdAt": "2024-01-15T10:30:00Z",
    "updatedAt": "2024-01-15T10:30:00Z"
  },
  {
    "id": "proj_660f9511f40c52e5",
    "name": "Production Analytics",
    "accountId": "550e8400-e29b-41d4-a716-446655440000",
    "createdAt": "2024-01-14T15:45:00Z",
    "updatedAt": "2024-01-14T15:45:00Z"
  }
]
```

---

### Update Stripe API Key
Store a Stripe API key for a project (used for revenue metrics).

```
PUT /api/v1/projects/:project_id/stripe-key
Authorization: Bearer <JWT_TOKEN>
Content-Type: application/json
```

**Path Parameters:**
- `project_id`: The project ID

**Request Body:**
```json
{
  "api_key": "sk_live_51234567890abcdef"
}
```

**Response (200 OK):**
```json
{
  "message": "Stripe API key updated successfully"
}
```

---

## API Key Management

### Create API Key
Generate a new API key for a project (used for event ingestion and integrations).

```
POST /api/v1/projects/:project_id/apikeys
Authorization: Bearer <JWT_TOKEN>
Content-Type: application/json
```

**Path Parameters:**
- `project_id`: The project ID

**Request Body:**
```json
{
  "name": "Production API Key",
  "permissions": ["read", "write"]
}
```

**Response (201 Created):**
```json
{
  "id": "key_550e8400e29b41d4",
  "name": "Production API Key",
  "key": "mentiq_live_8f5a9c2d1e7b6f4a3c9d2e1f0a5b8c7d",
  "permissions": ["read", "write"],
  "isActive": true,
  "projectId": "proj_550e8400e29b41d4",
  "createdAt": "2024-01-15T10:30:00Z"
}
```

**Important:** Store the `key` value securely. You won't be able to retrieve it again.

**Error Response (404 Not Found):**
```json
{
  "error": "Project not found"
}
```

---

## Analytics Endpoints

All analytics endpoints require authentication and the `X-Project-ID` header.

### Get Analytics
Retrieve aggregated analytics metrics for a project.

```
GET /api/v1/analytics?start_date=2024-01-01&end_date=2024-01-31&metrics=total_events,unique_users,page_views&group_by=day
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `start_date` | string | 7 days ago | Start date (YYYY-MM-DD) |
| `end_date` | string | Today | End date (YYYY-MM-DD) |
| `event_type` | string | - | Filter by event type |
| `user_id` | string | - | Filter by user ID |
| `session_id` | string | - | Filter by session ID |
| `metrics` | array | all | Comma-separated list of metrics |
| `group_by` | string | day | Grouping: hour, day, week, month |
| `limit` | integer | 100 | Result limit |
| `offset` | integer | 0 | Result offset |

**Available Metrics:**
- `total_events` - Total number of events
- `unique_users` - Number of unique users
- `top_events` - Most common event types
- `bounce_rate` - Percentage of single-event sessions
- `avg_session_duration` - Average session length
- `dau` - Daily Active Users
- `wau` - Weekly Active Users
- `mau` - Monthly Active Users
- `page_views` - Total page views
- `country_breakdown` - Events by country
- `city_breakdown` - Events by city
- `device_breakdown` - Events by device type
- `os_breakdown` - Events by operating system
- `browser_breakdown` - Events by browser
- `stickiness_ratio` - DAU/MAU ratio
- `session_frequency` - Average sessions per user
- `adoption_metrics` - Feature adoption rates
- `conversion_rate` - Conversion rate (Stripe)
- `churn_rate` - Churn rate (Stripe)
- `mrr` - Monthly Recurring Revenue (Stripe)
- `arpu` - Average Revenue Per User (Stripe)

**Response (200 OK):**
```json
{
  "query": {
    "start_date": "2024-01-01",
    "end_date": "2024-01-31",
    "event_type": "",
    "user_id": "",
    "session_id": "",
    "metrics": ["total_events", "unique_users"],
    "group_by": "day",
    "limit": 100,
    "offset": 0
  },
  "results": [
    {
      "metric": "total_events",
      "value": 15420,
      "time_series": [
        {
          "date": "2024-01-01",
          "value": 1200,
          "unique_users": 45
        },
        {
          "date": "2024-01-02",
          "value": 1450,
          "unique_users": 52
        }
      ]
    },
    {
      "metric": "unique_users",
      "value": 3284,
      "time_series": [
        {
          "date": "2024-01-01",
          "value": 45,
          "unique_users": 45
        },
        {
          "date": "2024-01-02",
          "value": 52,
          "unique_users": 52
        }
      ]
    }
  ],
  "meta": {
    "total_events": 15420,
    "processing_time": 245,
    "date_range": "2024-01-01 to 2024-01-31",
    "cache_hit": true
  }
}
```

---

### Get Dashboard
Get dashboard summary data for quick insights.

```
GET /api/v1/dashboard?date=2024-01-15
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Query Parameters:**
- `date`: Date for dashboard (YYYY-MM-DD), defaults to today

**Response (200 OK):**
```json
{
  "date": "2024-01-15",
  "overview": {
    "total_events_today": 142,
    "total_events_yesterday": 98,
    "unique_users_today": 67,
    "unique_users_yesterday": 45,
    "event_growth_rate": "+44.9%",
    "user_growth_rate": "+48.9%"
  },
  "user_metrics": {
    "dau": 67,
    "wau": 245,
    "mau": 1024
  },
  "page_metrics": {
    "page_views_today": 89,
    "page_views_yesterday": 67,
    "total_page_views": 2456
  },
  "top_events": [
    {
      "event_type": "page_view",
      "count": 89,
      "percentage": 62.7
    },
    {
      "event_type": "click",
      "count": 34,
      "percentage": 23.9
    },
    {
      "event_type": "form_submit",
      "count": 19,
      "percentage": 13.4
    }
  ],
  "meta": {
    "processing_time_ms": 125,
    "cache_hit": false
  }
}
```

---

### Get Real-Time Analytics
Get real-time metrics for current activity.

```
GET /api/v1/realtime
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "current_visitors": 23,
  "events_last_5_min": 45,
  "events_last_hour": 312,
  "cache_size": 1250,
  "top_pages_now": [
    {
      "page": "/dashboard",
      "visitors": 8
    },
    {
      "page": "/analytics",
      "visitors": 6
    },
    {
      "page": "/settings",
      "visitors": 3
    }
  ],
  "timestamp": "2024-01-15T10:30:00Z"
}
```

---

### Get User Metrics
Get DAU/WAU/MAU metrics with growth rates.

```
GET /api/v1/user-metrics?date=2024-01-15&metric=all
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Query Parameters:**
- `date`: Date for metrics (YYYY-MM-DD), defaults to today
- `metric`: Specific metric (dau, wau, mau) or "all" for all metrics

**Response (200 OK):**
```json
{
  "status": "success",
  "data": {
    "date": "2024-01-15",
    "dau": 67,
    "wau": 245,
    "mau": 1024,
    "dau_growth": "+12.3%",
    "wau_growth": "+8.7%",
    "mau_growth": "+15.2%"
  },
  "meta": {
    "processing_time_ms": 98,
    "cache_hit": false
  }
}
```

---

### Get Heatmap Data
Get click and scroll heatmap data for pages.

```
GET /api/v1/analytics/heatmaps
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "status": "success",
  "data": {
    "page_heatmaps": [
      {
        "page": "/dashboard",
        "clicks": [
          {
            "x": 150,
            "y": 200,
            "count": 45
          },
          {
            "x": 300,
            "y": 150,
            "count": 32
          }
        ]
      }
    ],
    "scroll_depth": {
      "25%": 0.89,
      "50%": 0.67,
      "75%": 0.45,
      "100%": 0.23
    }
  }
}
```

---

### Get Error Analytics
Get error tracking and analysis data.

```
GET /api/v1/analytics/errors
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "status": "success",
  "data": {
    "total_errors": 23,
    "error_rate": "1.2%",
    "top_errors": [
      {
        "error": "TypeError: Cannot read property 'x' of undefined",
        "count": 8
      },
      {
        "error": "NetworkError: Failed to fetch",
        "count": 6
      }
    ],
    "errors_by_browser": {
      "Chrome": 12,
      "Safari": 6,
      "Firefox": 3,
      "Edge": 2
    }
  }
}
```

---

### Get Session Analytics
Get detailed analytics for a specific user session.

```
GET /api/v1/sessions/:session_id
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Path Parameters:**
- `session_id`: The session ID to analyze

**Response (200 OK):**
```json
{
  "status": "success",
  "data": {
    "session_id": "sess_550e8400e29b41d4",
    "user_id": "user_123",
    "duration": "00:15:32",
    "page_views": 8,
    "events": [
      {
        "timestamp": "2024-01-15T10:30:00Z",
        "event_type": "page_view",
        "page": "/dashboard"
      },
      {
        "timestamp": "2024-01-15T10:32:15Z",
        "event_type": "click",
        "element": "export_button"
      }
    ],
    "device_info": {
      "browser": "Chrome",
      "os": "macOS",
      "device": "desktop"
    }
  }
}
```

---

### Get Retention Cohorts
Get user retention cohort analysis.

```
GET /api/v1/analytics/retention
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "status": "success",
  "data": {
    "cohorts": [
      {
        "cohort_date": "2024-01",
        "size": 100,
        "retention": {
          "week_1": 0.75,
          "week_2": 0.65,
          "week_3": 0.58,
          "week_4": 0.52
        }
      },
      {
        "cohort_date": "2024-02",
        "size": 120,
        "retention": {
          "week_1": 0.78,
          "week_2": 0.68,
          "week_3": 0.61,
          "week_4": 0.55
        }
      }
    ],
    "average_retention": {
      "week_1": 0.76,
      "week_2": 0.66,
      "week_3": 0.59,
      "week_4": 0.53
    }
  }
}
```

---

### Flush Cache
Manually trigger a cache flush (moves events from memory to storage).

```
POST /api/v1/flush-cache
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "message": "Cache flushed successfully",
  "events_processed": 1250,
  "new_cache_size": 0
}
```

**Response (200 OK - Empty Cache):**
```json
{
  "message": "Cache is empty, nothing to flush",
  "cache_size": 0
}
```

---

### Clear Data Cache
Clear all cached analytics data (forces fresh computation on next query).

```
POST /api/v1/clear-cache
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
{
  "message": "All data caches cleared successfully",
  "cleared": {
    "events_cache": 5,
    "dashboard_cache": 2,
    "metrics_cache": 8,
    "total": 15
  }
}
```

---

## A/B Testing Endpoints

All A/B testing endpoints require API key authentication.

### Create Experiment
Create a new A/B test experiment.

```
POST /api/v1/experiments
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
Content-Type: application/json
```

**Request Body:**
```json
{
  "name": "Homepage Button Color Test",
  "key": "homepage-button-color",
  "status": "DRAFT",
  "trafficSplit": 1.0,
  "projectId": "proj_550e8400e29b41d4",
  "description": "Testing if changing button color increases clicks",
  "startDate": "2024-01-20T00:00:00Z",
  "endDate": "2024-01-27T00:00:00Z",
  "variants": [
    {
      "name": "Control",
      "key": "control",
      "description": "Original blue button",
      "isControl": true,
      "trafficSplit": 0.5
    },
    {
      "name": "Red Button",
      "key": "red-button",
      "description": "Red button variant",
      "isControl": false,
      "trafficSplit": 0.5
    }
  ]
}
```

**Response (201 Created):**
```json
{
  "id": "exp_550e8400e29b41d4",
  "name": "Homepage Button Color Test",
  "description": "Testing if changing button color increases clicks",
  "key": "homepage-button-color",
  "status": "DRAFT",
  "traffic_split": 1.0,
  "start_date": "2024-01-20T00:00:00Z",
  "end_date": "2024-01-27T00:00:00Z",
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:30:00Z",
  "project_id": "proj_550e8400e29b41d4",
  "variants": [
    {
      "id": "var_550e8400e29b41d4",
      "name": "Control",
      "key": "control",
      "description": "Original blue button",
      "is_control": true,
      "traffic_split": 0.5,
      "created_at": "2024-01-15T10:30:00Z",
      "updated_at": "2024-01-15T10:30:00Z"
    },
    {
      "id": "var_660f9511f40c52e5",
      "name": "Red Button",
      "key": "red-button",
      "description": "Red button variant",
      "is_control": false,
      "traffic_split": 0.5,
      "created_at": "2024-01-15T10:30:00Z",
      "updated_at": "2024-01-15T10:30:00Z"
    }
  ]
}
```

---

### List Experiments
Get all experiments for a project.

```
GET /api/v1/experiments
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

**Response (200 OK):**
```json
[
  {
    "id": "exp_550e8400e29b41d4",
    "name": "Homepage Button Color Test",
    "description": "Testing if changing button color increases clicks",
    "key": "homepage-button-color",
    "status": "RUNNING",
    "traffic_split": 1.0,
    "start_date": "2024-01-20T00:00:00Z",
    "end_date": "2024-01-27T00:00:00Z",
    "created_at": "2024-01-15T10:30:00Z",
    "updated_at": "2024-01-15T10:30:00Z",
    "variants": [
      {
        "id": "var_550e8400e29b41d4",
        "name": "Control",
        "key": "control",
        "description": "Original blue button",
        "isControl": true,
        "trafficSplit": 0.5,
        "createdAt": "2024-01-15T10:30:00Z",
        "updatedAt": "2024-01-15T10:30:00Z"
      }
    ]
  }
]
```

---

### Get Experiment
Get details of a specific experiment.

```
GET /api/v1/experiments/:id
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

**Path Parameters:**
- `id`: The experiment ID

**Response (200 OK):**
```json
{
  "id": "exp_550e8400e29b41d4",
  "name": "Homepage Button Color Test",
  "description": "Testing if changing button color increases clicks",
  "key": "homepage-button-color",
  "status": "RUNNING",
  "traffic_split": 1.0,
  "start_date": "2024-01-20T00:00:00Z",
  "end_date": "2024-01-27T00:00:00Z",
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:30:00Z",
  "variants": [...]
}
```

---

### Get Variant Assignment
Get the assigned variant for a user in an experiment.

```
POST /api/v1/experiments/:experimentKey/assignment?experimentKey=<KEY>&projectId=<PROJECT_ID>&userId=<USER_ID>
Authorization: Bearer <API_KEY>
```

**Query Parameters:**
- `experimentKey`: The experiment key
- `projectId`: The project ID
- `userId`: (Optional) The user ID
- `anonymousId`: (Optional) The anonymous ID (use if userId not available)

**Response (200 OK):**
```json
{
  "id": "var_550e8400e29b41d4",
  "name": "Red Button",
  "key": "red-button",
  "description": "Red button variant",
  "is_control": false,
  "traffic_split": 0.5,
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:30:00Z"
}
```

---

### Track Conversion
Record a conversion event for a user in an experiment.

```
POST /api/v1/experiments/track
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
Content-Type: application/json
```

**Request Body:**
```json
{
  "experimentId": "exp_550e8400e29b41d4",
  "eventName": "button_click",
  "userId": "user_123",
  "value": 1.5,
  "properties": {
    "page": "/homepage",
    "section": "hero"
  }
}
```

**Note:** Either `userId` or `anonymousId` is required.

**Response (200 OK):**
```json
{
  "status": "ok"
}
```

---

### Get Experiment Results
Get aggregated results for an experiment.

```
GET /api/v1/experiments/:id/results
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

**Path Parameters:**
- `id`: The experiment ID

**Response (200 OK):**
```json
[
  {
    "variantId": "var_550e8400e29b41d4",
    "totalConversions": 1250,
    "totalValue": 1875.50,
    "uniqueUsers": 1200
  },
  {
    "variantId": "var_660f9511f40c52e5",
    "totalConversions": 1420,
    "totalValue": 2130.75,
    "uniqueUsers": 1350
  }
]
```

---

### Update Experiment Status
Change the status of an experiment.

```
PUT /api/v1/experiments/:id/status
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

**Path Parameters:**
- `id`: The experiment ID

**Query Parameters:**
- `status`: One of: DRAFT, RUNNING, PAUSED, COMPLETED, ARCHIVED

**Response (200 OK):**
```json
{
  "status": "success"
}
```

**Valid Status Transitions:**
- DRAFT → RUNNING, ARCHIVED
- RUNNING → PAUSED, COMPLETED, ARCHIVED
- PAUSED → RUNNING, ARCHIVED
- COMPLETED → ARCHIVED
- ARCHIVED → (no transitions)

---

## Error Handling

### Error Response Format

All error responses follow this format:

```json
{
  "error": "Description of what went wrong"
}
```

### Common Error Codes

| Status Code | Description |
|-------------|-------------|
| `400 Bad Request` | Invalid request parameters or validation error |
| `401 Unauthorized` | Missing or invalid authentication token |
| `403 Forbidden` | Authenticated user lacks permission |
| `404 Not Found` | Resource not found |
| `409 Conflict` | Resource already exists (e.g., duplicate email) |
| `500 Internal Server Error` | Server error |

### Common Error Messages

```json
{
  "error": "Authorization header required"
}
```

```json
{
  "error": "Invalid token"
}
```

```json
{
  "error": "Token expired"
}
```

```json
{
  "error": "Project not found"
}
```

```json
{
  "error": "Email already in use"
}
```

```json
{
  "error": "Invalid credentials"
}
```

---

## Rate Limiting

Currently no rate limiting is implemented. Production deployment should implement rate limiting per API key.

---

## Pagination

List endpoints support pagination via query parameters:
- `limit`: Number of results (default: 100, max: 1000)
- `offset`: Number of results to skip (default: 0)

Example:
```
GET /api/v1/projects?limit=50&offset=100
```

---

## Timestamps

All timestamps are in ISO 8601 format with UTC timezone:
```
2024-01-15T10:30:00Z
```

---

## Examples

### Complete Workflow Example

1. **Sign up:**
```bash
curl -X POST http://localhost:8080/signup \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Jane Doe",
    "email": "jane@example.com",
    "password": "securepass123"
  }'
```

2. **Login:**
```bash
curl -X POST http://localhost:8080/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "jane@example.com",
    "password": "securepass123"
  }'
# Save the token from response
```

3. **Create project:**
```bash
curl -X POST http://localhost:8080/api/v1/projects \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Analytics Project"
  }'
# Save the project_id from response
```

4. **Create API key:**
```bash
curl -X POST http://localhost:8080/api/v1/projects/<PROJECT_ID>/apikeys \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Production Key",
    "permissions": ["read", "write"]
  }'
# Save the api_key from response
```

5. **Create experiment:**
```bash
curl -X POST http://localhost:8080/api/v1/experiments \
  -H "Authorization: Bearer <API_KEY>" \
  -H "X-Project-ID: <PROJECT_ID>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Color Test",
    "key": "color-test",
    "status": "DRAFT",
    "trafficSplit": 1.0,
    "projectId": "<PROJECT_ID>",
    "variants": [
      {"name": "Control", "key": "control", "isControl": true, "trafficSplit": 0.5},
      {"name": "Red", "key": "red", "isControl": false, "trafficSplit": 0.5}
    ]
  }'
```

6. **Get analytics:**
```bash
curl -X GET "http://localhost:8080/api/v1/analytics?start_date=2024-01-01&metrics=total_events,unique_users" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "X-Project-ID: <PROJECT_ID>"
```

---

## Support

For issues or questions, please refer to the main README.md or contact support.
