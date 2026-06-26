# Stage 1: Build
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main cmd/api/main.go

# Stage 2: Run
FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/main .
RUN chmod +x /app/main && \
    addgroup -S appgroup && \
    adduser -S appuser -G appgroup
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1
EXPOSE 8080
USER appuser
CMD ["./main"]
