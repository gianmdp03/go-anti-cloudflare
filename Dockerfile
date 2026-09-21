# ==============================================================================
# STAGE 1: Build static Go binaries
# ==============================================================================
FROM golang:alpine AS builder

# Install build dependencies, CA certificates and timezone data
RUN apk update && apk add --no-cache ca-certificates tzdata git

# Create unprivileged non-root user (UID: 10001, GID: 10001)
RUN addgroup -g 10001 appgroup && \
    adduser -u 10001 -G appgroup -s /sbin/nologin -D -H appuser

WORKDIR /build

# Cache module dependencies layer
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy application source tree
COPY . .

# Compile high-performance static binaries
# -ldflags="-w -s" strips DWARF symbol tables and debug symbols
# -trimpath removes sensitive local path metadata from stack traces
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

RUN go build \
    -trimpath \
    -ldflags="-w -s -extldflags '-static'" \
    -o /build/mgp-proxy \
    ./cmd/server

RUN go build \
    -trimpath \
    -ldflags="-w -s -extldflags '-static'" \
    -o /build/healthcheck \
    ./cmd/healthcheck

# ==============================================================================
# STAGE 2: Microscopic production scratch runtime
# ==============================================================================
FROM scratch

# Import TLS certificates and timezone database from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Import unprivileged user credentials
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy compiled binaries
COPY --from=builder /build/mgp-proxy /mgp-proxy
COPY --from=builder /build/healthcheck /healthcheck

# Run as non-root user
USER 10001:10001

# Default environment configuration
ENV PORT=8080 \
    UPSTREAM_URL=https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php \
    TIMEOUT_SECONDS=45 \
    ALLOWED_ORIGIN=http://localhost:8400 \
    TLS_PROFILE=Chrome_131 \
    LOG_LEVEL=info \
    AUTH_SERVICE_URL=http://localhost:8191/v1

EXPOSE 8080

# Native healthcheck using compiled Go probe (works in scratch without curl/sh)
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 CMD ["/healthcheck"]

ENTRYPOINT ["/mgp-proxy"]
