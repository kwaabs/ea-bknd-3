# -----------------------------
# Stage 1: Build the Go binary
# -----------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy module files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

# -----------------------------
# Stage 2: Runtime image
# -----------------------------
FROM alpine:3.20

# Install CA certificates
RUN apk --no-cache add ca-certificates

# Create a non-root user
RUN adduser -D -g '' appuser

# Set working directory
WORKDIR /app

# Copy the binary
COPY --from=builder /app/server .

# Copy any runtime assets (keys, certs, etc.)
COPY --from=builder /app/keys ./keys

# .env is NOT copied.
# In production (Coolify), configuration is supplied through
# environment variables, not a physical .env file.
#
# COPY --from=builder /app/.env .

# Give ownership to the application user
RUN chown -R appuser:appuser /app

# Run as non-root
USER appuser

# Expose application port
EXPOSE 8780

# Default port
ENV PORT=8780

# Start the application
ENTRYPOINT ["./server"]
