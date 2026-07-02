# syntax=docker/dockerfile:1

# --- Build stage -----------------------------------------------------------
FROM golang:1.24-bookworm AS build

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/image-detection .

# --- Runtime stage -----------------------------------------------------------
FROM debian:13-slim

# ca-certificates is required for outbound TLS calls to GCS and Alibaba
# Cloud APIs.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates wget \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --uid 1000 --create-home --home-dir /app --shell /usr/sbin/nologin appuser

WORKDIR /app

COPY --from=build /out/image-detection ./image-detection
COPY htdocs ./htdocs

# logs/ is where request logging writes; mount a volume here in
# docker-compose to persist it outside the container.
RUN mkdir -p logs && chown -R appuser:appuser /app

USER appuser

EXPOSE 8080

# .env is expected to be provided via a mounted file or docker-compose
# env_file / environment entries — see docker-compose.yml. The app also
# reads real environment variables directly (which take precedence).
ENTRYPOINT ["./image-detection"]
