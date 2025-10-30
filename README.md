# Mentiq Analytics Platform Backend

A high-performance analytics platform backend service built in Go with GORM ORM, JWT authentication, and AWS S3 storage.

## Features

- **Event Ingestion**: REST API endpoints for single and batch event ingestion
- **Session Recording**: Capture and replay user sessions using rrweb
- **JWT Authentication**: Secure token-based authentication with 24-hour expiration
- **API Key Management**: Project-scoped API keys for client-side SDKs
- **Advanced Analytics**: DAU, WAU, MAU, page views, retention cohorts, and more
- **A/B Testing**: Complete experimentation framework with variant assignment and conversion tracking
- **Heatmap Analytics**: Visualize user click patterns
- **Error Analytics**: Aggregate and track frontend errors
- **Session Analytics**: Detailed session-level analytics
- **S3/R2 Storage**: Scalable event and recording storage
- **PostgreSQL/Supabase**: GORM-based database with auto-migrations
- **CORS Support**: Configurable Cross-Origin Resource Sharing
- **Health Monitoring**: Health check endpoint for monitoring

## Documentation

- **[API Documentation](./API_DOCUMENTATION.md)**: Complete API reference for all endpoints
- **[API Quick Reference](./API_QUICK_REFERENCE.md)**: Quick curl command examples
- **[Session Recording Integration](./SESSION_RECORDING_INTEGRATION.md)**: Frontend SDK integration guide
- **[Session Recording Implementation](./SESSION_RECORDING_IMPLEMENTATION.md)**: Backend implementation details

## API Endpoints Overview

For complete API documentation, see [API_DOCUMENTATION.md](./API_DOCUMENTATION.md).

### Authentication

#### Signup

```
POST /signup
```

#### Login

```
POST /login
```

Returns JWT token valid for 24 hours.

### Project Management

#### Create Project

```
POST /api/v1/projects
Authorization: Bearer <JWT_TOKEN>
```

#### List Projects

```
GET /api/v1/projects
Authorization: Bearer <JWT_TOKEN>
```

#### Create API Key

```
POST /api/v1/projects/:project_id/apikeys
Authorization: Bearer <JWT_TOKEN>
```

### Analytics

##### Ingest Events

```
POST /api/v1/events
Authorization: Bearer <API_KEY>
```

#### Get Analytics

```
GET /api/v1/analytics?start_date=2024-01-01&end_date=2024-01-31
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

### Session Recording

#### Ingest Recording

```
POST /api/v1/sessions/:session_id/recordings
Authorization: Bearer <API_KEY>
```

#### List Recordings

```
GET /api/v1/recordings?project_id=<PROJECT_ID>
Authorization: Bearer <JWT_TOKEN>
```

#### Get Recording

```
GET /api/v1/recordings/:id
Authorization: Bearer <JWT_TOKEN>
```

### A/B Testing

#### Create Experiment

```
POST /api/v1/experiments
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

#### Get Assignment

```
POST /api/v1/experiments/:experimentKey/assignment
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

#### Track Conversion

```
POST /api/v1/experiments/track
Authorization: Bearer <API_KEY>
X-Project-ID: <PROJECT_ID>
```

#### Get Results

```
GET /api/v1/experiments/:id/results
Authorization: Bearer <JWT_TOKEN>
X-Project-ID: <PROJECT_ID>
```

For complete endpoint details and examples, see [API_DOCUMENTATION.md](./API_DOCUMENTATION.md).

## Event Schema

### Batch Event Ingestion

```
POST /api/v1/events/batch
Content-Type: application/json

[
  {
    "event_type": "page_view",
    "user_id": "user123",
    "properties": {"page": "/home"}
  },
  {
    "event_type": "click",
    "user_id": "user123",
    "properties": {"element": "button", "text": "Sign Up"}
  }
]
```

## Event Schema

```json
{
  "event_id": "auto-generated UUID if not provided",
  "event_type": "required - type of event (page_view, click, etc.)",
  "user_id": "optional - user identifier",
  "session_id": "optional - session identifier",
  "timestamp": "auto-generated UTC timestamp if not provided",
  "properties": "optional - custom event properties",
  "user_agent": "auto-extracted from request headers",
  "ip_address": "auto-extracted from request"
}
```

## Environment Variables

| Variable                | Description                       | Required | Default     |
| ----------------------- | --------------------------------- | -------- | ----------- |
| `DATABASE_URL`          | PostgreSQL connection string      | Yes      | -           |
| `S3_BUCKET_NAME`        | Name of the S3/R2 bucket          | Yes      | -           |
| `AWS_REGION`            | AWS region for S3 bucket          | Yes      | -           |
| `AWS_ACCESS_KEY_ID`     | AWS access key                    | Yes      | -           |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key                    | Yes      | -           |
| `AWS_ENDPOINT`          | Custom S3 endpoint (for R2/MinIO) | No       | AWS default |
| `PORT`                  | Port number for the server        | No       | 8080        |
| `CORS_DEBUG`            | Enable CORS debugging             | No       | false       |

### Supabase Integration

To use Supabase as your PostgreSQL database:

```bash
DATABASE_URL=postgresql://postgres:[YOUR-PASSWORD]@db.[YOUR-PROJECT-REF].supabase.co:5432/postgres
```

## Setup and Installation

1. **Clone and navigate to the project:**

   ```bash
   cd mentiq-backend
   ```

2. **Install dependencies:**

   ```bash
   go mod tidy
   ```

3. **Set up environment variables:**

   ```bash
   cp .env.example .env
   # Edit .env with your configuration
   ```

4. **Configure Database:**

   For Supabase:

   ```bash
   DATABASE_URL=postgresql://postgres:[YOUR-PASSWORD]@db.[YOUR-PROJECT-REF].supabase.co:5432/postgres
   ```

   For local PostgreSQL:

   ```bash
   DATABASE_URL=postgresql://user:password@localhost:5432/mentiq_db
   ```

5. **Configure AWS/R2 credentials:**

   For AWS S3:

   ```bash
   AWS_ACCESS_KEY_ID=your-access-key
   AWS_SECRET_ACCESS_KEY=your-secret-key
   AWS_REGION=us-east-1
   S3_BUCKET_NAME=your-bucket-name
   ```

   For Cloudflare R2:

   ```bash
   AWS_ACCESS_KEY_ID=your-r2-access-key
   AWS_SECRET_ACCESS_KEY=your-r2-secret-key
   AWS_ENDPOINT=https://[account-id].r2.cloudflarestorage.com
   AWS_REGION=auto
   S3_BUCKET_NAME=your-bucket-name
   ```

6. **Run migrations (automatic on startup):**

   The application will automatically create tables and indices on first run.

7. **Run the application:**

   ```bash
   go run main.go
   # or using make
   make run
   ```

8. **Test the setup:**

   ```bash
   # Check health
   curl http://localhost:8080/health

   # Create account
   curl -X POST http://localhost:8080/signup \
     -H "Content-Type: application/json" \
     -d '{"name":"Test User","email":"test@example.com","password":"password123"}'
   ```

## S3/R2 Storage Structure

### Events Storage

Events are stored in S3 with the following partitioned structure:

```
events/
├── year=2024/
│   ├── month=01/
│   │   ├── day=15/
│   │   │   ├── event-uuid-1.json
│   │   │   ├── event-uuid-2.json
│   │   │   └── ...
│   │   └── day=16/
│   └── month=02/
└── year=2025/
```

### Session Recordings Storage

Recordings are stored with project and session organization:

```
recordings/
├── proj_123/
│   ├── sess_abc/
│   │   └── rec_xyz.json
│   └── sess_def/
│       └── rec_uvw.json
└── proj_456/
    └── sess_ghi/
        └── rec_rst.json
```

This structure enables efficient querying and processing with tools like AWS Athena or Apache Spark.

## Database Schema

The application uses GORM for ORM and automatically creates the following tables:

- `account`: User accounts with JWT authentication
- `user`: End-user tracking (for analytics)
- `project`: Projects within accounts
- `api_key`: API keys for project-scoped authentication
- `experiment`: A/B test experiments
- `variant`: Experiment variants (control/treatment)
- `experiment_assignment`: User assignments to variants
- `conversion_event`: Conversion tracking for experiments
- `session_recording`: Session recording metadata

Migrations run automatically on application startup.

## Production Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o analytics-platform main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/analytics-platform .
CMD ["./analytics-platform"]
```

### CORS Configuration

For production, update the CORS configuration in `main.go`:

```go
AllowedOrigins: []string{"https://yourdomain.com", "https://app.yourdomain.com"}
```

### Security Considerations

- Configure appropriate CORS origins for production
- Use IAM roles with minimal required permissions
- Implement rate limiting for production use
- Add authentication/authorization as needed
- Monitor and log all requests

## Monitoring and Logging

The service logs important events including:

- Event ingestion success/failure
- S3 storage operations
- Server startup information
- Error conditions

## Performance

- Concurrent event processing with goroutines
- Efficient JSON marshaling/unmarshaling
- Streaming uploads to S3
- Connection pooling for HTTP requests

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request
