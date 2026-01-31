# Use the official Golang image to build the application
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGo, SQLite, and Olm (for Matrix E2EE)
RUN apk add --no-cache gcc g++ musl-dev sqlite-dev olm-dev

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY src/ ./src/

# Build the Go app with CGo enabled for SQLite support
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -a -ldflags="-w -s" -o nextcloud-media-bridge ./src

# Use a minimal image for the final container
FROM alpine:latest

# Install ca-certificates for HTTPS connections, SQLite runtime, and Olm runtime libraries
RUN apk --no-cache add ca-certificates sqlite-libs olm

# Create a non-root user
RUN addgroup -g 1000 bridge && \
    adduser -D -u 1000 -G bridge bridge

# Set the working directory
WORKDIR /app

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/nextcloud-media-bridge .

# Change ownership
RUN chown bridge:bridge /app/nextcloud-media-bridge

# Switch to non-root user
USER bridge

# Expose ports (appservice and media proxy)
EXPOSE 29334 29335

# Command to run the executable
CMD ["./nextcloud-media-bridge"]