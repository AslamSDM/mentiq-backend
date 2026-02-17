# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o mentiq-backend .

# Runtime stage
FROM alpine:3.20

RUN apk --no-cache add ca-certificates wget
RUN adduser -D -s /bin/sh mentiq

WORKDIR /home/mentiq
COPY --from=builder /app/mentiq-backend .
RUN chown mentiq:mentiq mentiq-backend

USER mentiq

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./mentiq-backend"]
