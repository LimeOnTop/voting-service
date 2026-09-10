# syntax=docker/dockerfile:1.4

FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build -o voting-api ./cmd/api \
	&& CGO_ENABLED=0 GOOS=linux go build -o voting-sync ./cmd/worker

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata \
	&& adduser -D -s /bin/sh appuser

WORKDIR /app

COPY --from=builder /app/voting-api /app/voting-sync ./

RUN chown -R appuser:appuser /app

USER appuser

EXPOSE 8080
CMD ["./voting-api"]
