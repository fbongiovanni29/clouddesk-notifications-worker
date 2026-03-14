# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /notifications-worker .

# ── Runtime stage ──────────────────────────────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /notifications-worker /usr/local/bin/notifications-worker
EXPOSE 8080
ENTRYPOINT ["notifications-worker"]
