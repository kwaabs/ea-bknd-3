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

# Add CA certs FIRST (as root)
RUN apk --no-cache add ca-certificates

# Create a non-root user
RUN adduser -D -g '' appuser

# Set working directory
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/server .

# Copy the keys
COPY --from=builder /app/keys ./keys

# In Stage 2, after COPY keys
COPY --from=builder /app/.env .

# Change ownership to appuser
RUN chown -R appuser:appuser /app

# NOW switch to non-root user
USER appuser

# Expose your correct port
EXPOSE 8780

# Set environment variable
ENV PORT=8780

# Use ENTRYPOINT to make the container behave as the executable
ENTRYPOINT ["./server"]