# User Metrics Implementation

This document describes the implementation of Daily Active Users (DAU), Weekly Active Users (WAU), Monthly Active Users (MAU), and Page View metrics in the analytics platform.

## New Metrics Added

### 1. Daily Active Users (DAU)

- **Definition**: Unique users who performed any action on the current day
- **Calculation**: Count of unique `user_id` values for events on the current date
- **Time Series**: Available with hourly, daily, weekly, and monthly grouping

### 2. Weekly Active Users (WAU)

- **Definition**: Unique users who performed any action in the last 7 days
- **Calculation**: Count of unique `user_id` values for events in the last 7 days
- **Time Series**: Available with weekly and monthly grouping

### 3. Monthly Active Users (MAU)

- **Definition**: Unique users who performed any action in the last 30 days
- **Calculation**: Count of unique `user_id` values for events in the last 30 days
- **Time Series**: Available with monthly grouping

### 4. Page View Counts

- **Definition**: Count of events with type "page_view" or "pageview"
- **Breakdown**: Available by page path/URL (extracted from event properties)
- **Time Series**: Available with all grouping options

## API Endpoints

### 1. Analytics Endpoint (Enhanced)

- **URL**: `GET /api/v1/analytics`
- **Query Parameters**:
  - `metrics`: Array of metrics to calculate (now includes `dau`, `wau`, `mau`, `page_views`)
  - `start_date`: Start date for analysis (YYYY-MM-DD)
  - `end_date`: End date for analysis (YYYY-MM-DD)
  - `group_by`: Time grouping (hour, day, week, month)

Example request:

```
GET /api/v1/analytics?metrics=dau,wau,mau,page_views&start_date=2025-09-01&end_date=2025-09-14&group_by=day
```

### 2. Dashboard Endpoint (Enhanced)

- **URL**: `GET /api/v1/dashboard`
- **Response**: Enhanced with user metrics and page metrics sections

Example response:

```json
{
  "overview": {
    "total_events_today": 150,
    "total_events_yesterday": 120,
    "unique_users_today": 45,
    "unique_users_yesterday": 38,
    "event_growth_rate": "25.0%",
    "user_growth_rate": "18.4%"
  },
  "user_metrics": {
    "dau": 45,
    "wau": 312,
    "mau": 1250
  },
  "page_metrics": {
    "page_views_today": 89,
    "page_views_yesterday": 76,
    "total_page_views": 2340
  }
}
```

### 3. User Metrics Endpoint (New)

- **URL**: `GET /api/v1/user-metrics`
- **Query Parameters**:
  - `start_date`: Start date for analysis (default: 30 days ago)
  - `end_date`: End date for analysis (default: today)
  - `group_by`: Time grouping (default: day)

Example response:

```json
{
  "current_metrics": {
    "dau": 45,
    "wau": 312,
    "mau": 1250
  },
  "time_series": {
    "dau": [
      {"timestamp": "2025-09-01T00:00:00Z", "value": 42},
      {"timestamp": "2025-09-02T00:00:00Z", "value": 38}
    ],
    "wau": [...],
    "mau": [...]
  },
  "date_range": {
    "start_date": "2025-08-15",
    "end_date": "2025-09-14",
    "group_by": "day"
  }
}
```

## Implementation Details

### Data Requirements

- Events must have a `user_id` field to be counted in user metrics
- Page view events should have type "page_view" or "pageview"
- Page path/URL should be stored in event properties as "path", "url", or "page"

### Performance Considerations

- Metrics are calculated from events stored in S3
- Large date ranges may take longer to process
- Consider using the caching system for frequently accessed metrics

### Time Series Calculations

- **DAU Time Series**: Unique users per time period
- **WAU Time Series**: Rolling 7-day unique users for each time period
- **MAU Time Series**: Rolling 30-day unique users for each time period
- **Page Views Time Series**: Total page view events per time period

## Usage Examples

### Get User Growth Trends

```bash
curl -H "Authorization: ApiKey YOUR_API_KEY" \
     -H "X-Project-ID: YOUR_PROJECT_ID" \
     "http://localhost:8080/api/v1/user-metrics?start_date=2025-08-01&end_date=2025-09-14&group_by=week"
```

### Get Page Performance Analytics

```bash
curl -H "Authorization: ApiKey YOUR_API_KEY" \
     -H "X-Project-ID: YOUR_PROJECT_ID" \
     "http://localhost:8080/api/v1/analytics?metrics=page_views&start_date=2025-09-01&end_date=2025-09-14"
```

### Get Complete Dashboard

```bash
curl -H "Authorization: ApiKey YOUR_API_KEY" \
     -H "X-Project-ID: YOUR_PROJECT_ID" \
     "http://localhost:8080/api/v1/dashboard"
```

## Testing

The implementation includes comprehensive tests covering:

- User metrics calculation accuracy
- API endpoint responses
- Time series data generation
- Edge cases (empty data, missing user IDs)

Run tests with:

```bash
go test -v
```

## Future Enhancements

1. **Retention Analysis**: Calculate user retention rates (Day 1, Day 7, Day 30)
2. **Cohort Analysis**: Track user behavior across different user cohorts
3. **Funnel Analysis**: Track users through conversion funnels
4. **Real-time Metrics**: Calculate metrics from cached events for real-time insights
5. **Comparative Analytics**: Compare metrics across different time periods
