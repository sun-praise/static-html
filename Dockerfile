# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /html-server ./cmd/html-server

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

RUN adduser -D -u 1000 appuser

WORKDIR /app

COPY --from=builder /html-server /usr/local/bin/html-server

USER appuser

EXPOSE 3939

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:3939/ || exit 1

ENTRYPOINT ["html-server"]
CMD ["start", "--host", "0.0.0.0", "--port", "3939"]
