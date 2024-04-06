# Use an official Golang runtime as a parent image
FROM golang:1.21 AS builder

# Set the current working directory inside the container
WORKDIR /app

# Copy the Go module files
COPY go.mod .

# Download and install dependencies
RUN go mod download

# Copy the source code into the container
COPY main.go .
COPY ./config ./config

# Build the Go application
RUN go build -o rewriteblock .

# Start a new stage from alpine
FROM alpine:latest

ENV THANOS_VERSION=0.34.1
ENV THANOS_URL=https://github.com/thanos-io/thanos/releases/download/v${THANOS_VERSION}/thanos-${THANOS_VERSION}.linux-amd64.tar.gz

# Download and extract Thanos
RUN wget -O thanos.tar.gz ${THANOS_URL} && \
    tar -xvf thanos.tar.gz && \
    cp thanos-${THANOS_VERSION}.linux-amd64/thanos /usr/local/bin/ && \
    chmod +x /usr/local/bin/thanos && \
    rm -rf thanos.tar.gz thanos-${THANOS_VERSION}.linux-amd64

# Set the working directory in the container
WORKDIR /app

# Copy the executable from the builder stage
COPY --from=builder /app/rewriteblock .

# Define the entrypoint command to run when the container starts
ENTRYPOINT ["rewriteblock"]