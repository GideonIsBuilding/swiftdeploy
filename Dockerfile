# Stage 1: Build 
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY app/go.mod ./
RUN go mod download

COPY app/ ./

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app .

# Stage 2: Run
FROM alpine:3.19

RUN adduser -D -u 1000 appuser

RUN apk add --no-cache wget

WORKDIR /app

COPY --from=builder /app ./app

RUN mkdir -p /var/log/app && chown appuser:appuser /var/log/app

USER appuser

EXPOSE 3000

CMD ["./app"]
