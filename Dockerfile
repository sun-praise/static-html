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

ENTRYPOINT ["html-server"]
