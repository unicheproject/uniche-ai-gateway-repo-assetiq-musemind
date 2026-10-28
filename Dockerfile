# Build stage
FROM golang:1.26 AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Install codegen tools used by `go generate` and templ.
# NOTE: keep templ's version in sync with github.com/a-h/templ in go.mod — mismatch causes
# "undefined: templ.ResolveAttributeValue" build errors.
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020 && \
    go install github.com/ogen-go/ogen/cmd/ogen@v1.20.3

# Generate templ templates and ogen REST types (mirrors `make build`)
RUN templ generate
RUN go generate ./...

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o uniche ./cmd/server

# Runtime stage
FROM debian:12-slim

# Install runtime dependencies
# ca-certificates: for HTTPS
# tzdata: for proper timezone handling
# wget: for health checks
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Create app user for security
RUN groupadd -g 1000 uniche && \
    useradd -r -u 1000 -g uniche uniche

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/uniche .

# Create directory for the persistent SQLite database
RUN mkdir -p /app/uniche-data && \
    chown -R uniche:uniche /app

# Switch to non-root user
USER uniche

ENV DB_PATH=/app/uniche-data/uniche.db

EXPOSE 8080 8081

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

CMD ["./uniche"]
