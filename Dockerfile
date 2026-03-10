# Stage 1: Build
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git ca-certificates

# Copy go.mod/go.sum first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o /server ./cmd/server

# Stage 2: Runtime
FROM gcr.io/distroless/static-debian12

COPY --from=builder /server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
