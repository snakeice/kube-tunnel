# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache \
    ca-certificates \
    git \
    tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY ./go.mod ./go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# Set environment variables for cross-compilation
ENV GOOS=$TARGETOS
ENV GOARCH=$TARGETARCH

# Build the binary
RUN CGO_ENABLED=0 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o kube-tunnel ./cmd

# Final stage - minimal runtime image
FROM scratch

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the binary
COPY --from=builder /app/kube-tunnel /usr/local/bin/kube-tunnel

# Create non-root user
USER 65534:65534

# Expose default port
EXPOSE 80

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/kube-tunnel", "-help"]

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/kube-tunnel"]
