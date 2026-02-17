# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -o meme-bot ./cmd

# Runtime stage
FROM alpine:latest

# Install runtime dependencies (ffmpeg, ca-certificates, and other runtime libs)
RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    curl \
    libc6-compat

# Create app user for security
RUN addgroup -g 1000 memebot && \
    adduser -D -u 1000 -G memebot memebot

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/meme-bot .

# Change to non-root user
USER memebot

EXPOSE 8000

# Run the application
CMD ["./meme-bot"]