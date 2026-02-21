# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o filetransfer ./cmd/app/main.go

# Run stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies (if any)
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/filetransfer .

# Create downloads directory
RUN mkdir -p downloads

# Expose ports
# 8080: Web UI
# 9000: File Transfer TCP
# 9001: Discovery UDP
EXPOSE 8080 9000 9001/udp 9001/tcp

# Set environment variables (defaults)
ENV DATABASE_URL="host=db port=5432 user=sameer password=Sameer@123 dbname=filetransfer sslmode=disable"

# Run the application
CMD ["./filetransfer"]
