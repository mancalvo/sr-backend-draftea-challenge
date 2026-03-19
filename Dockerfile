# Multi-stage Dockerfile for all Go services.
# Build with: docker build --build-arg SERVICE=<name> -t <name> .
# where <name> is one of: api-gateway, saga-orchestrator, payments, wallets, catalog-access

# ---- build stage ----
FROM golang:1.25-alpine AS builder

ARG SERVICE

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app ./cmd/${SERVICE}

# ---- runtime stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app /app

ENTRYPOINT ["/app"]
