# Use an official Go runtime as a parent image
FROM golang:1.23 AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy the Go Modules manifests
COPY go.mod go.sum ./

# Download dependencies
RUN go mod tidy

# Copy the source code into the container
COPY main.go main.go

# Build the Go app
RUN go build -o myapp .

# Start a new stage from scratch to copy only the binary
FROM alpine:latest  

WORKDIR /root/

# Copy the pre-built binary file from the builder stage
COPY --from=builder /app/myapp .

# Run the Go app
CMD ["./myapp"]
