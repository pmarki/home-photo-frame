# ── Stage 1: build frontend ───────────────────────────────────────────────────
FROM node:22-alpine AS frontend

WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── Stage 2: build Go binary ──────────────────────────────────────────────────
FROM golang:1.22-alpine AS backend

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY --from=frontend /app/frontend/dist ./frontend/dist
RUN go build -o photo-frame .

# ── Stage 3: minimal runtime image ───────────────────────────────────────────
FROM alpine:3.20

# ffmpeg is only needed when VIDEO=1; included here so the image is ready
# without rebuilding. Remove this line if you want a smaller image and
# don't plan to use video support.
RUN apk add --no-cache ffmpeg

WORKDIR /app
COPY --from=backend /app/photo-frame ./

# Defaults — all overridable via environment variables or docker-compose.
ENV PHOTOS_DIR=/data/photos \
    CACHE_DIR=/data/cache \
    PORT=8080

VOLUME ["/data/photos", "/data/cache"]
EXPOSE 8080

ENTRYPOINT ["./photo-frame"]
