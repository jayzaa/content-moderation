#!/usr/bin/env bash
#
# install.sh — set up image-detection on a fresh Debian 13 host.
#
# What this does:
#   1. Installs the Go toolchain (apt: golang-go) if not already present.
#   2. Installs nginx (apt) if not already present.
#   3. Builds the image-detection binary.
#   4. Copies .env.example to .env if .env doesn't exist yet (you still
#      need to edit it and fill in real credentials — this script does
#      NOT and cannot know your GCS bucket, Alibaba keys, etc).
#
# This script does NOT:
#   - fill in any credentials for you
#   - start the app or configure a systemd service
#   - configure the nginx virtual host (see nginx/image-detection.conf.sample)
#
# Usage:
#   ./install.sh
#
# Run from the root of a cloned copy of this repository.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

log() { printf '\033[1;32m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m==>\033[0m %s\n' "$1"; }

if [[ "$(id -u)" -eq 0 ]]; then
  warn "Running as root. apt commands below will run directly;" \
       "everything else (build, .env) runs as the invoking user."
fi

SUDO=""
if [[ "$(id -u)" -ne 0 ]]; then
  SUDO="sudo"
fi

log "Updating apt package index..."
$SUDO apt update

if ! command -v go >/dev/null 2>&1; then
  log "Installing Go toolchain (golang-go)..."
  $SUDO apt install -y golang-go
else
  log "Go toolchain already installed: $(go version)"
fi

if ! command -v nginx >/dev/null 2>&1; then
  log "Installing nginx..."
  $SUDO apt install -y nginx
else
  log "nginx already installed."
fi

log "Downloading Go module dependencies..."
go mod download

log "Building bin/image-detection..."
mkdir -p bin
go build -o bin/image-detection .
log "Build complete: bin/image-detection"

if [[ ! -f .env ]]; then
  log "No .env found — copying .env.example to .env"
  cp .env.example .env
  warn ".env created from template. You MUST edit it and fill in:"
  warn "  API_BEARER_TOKEN, GCP_PROJECT_ID, GCS_BUCKET, GCS_CREDENTIALS_FILE,"
  warn "  ALIBABA_CLOUD_ACCESS_KEY_ID, ALIBABA_CLOUD_ACCESS_KEY_SECRET"
  warn "before running the app. See README.md for details on each variable."
else
  log ".env already exists — leaving it untouched."
fi

cat <<'EOF'

Next steps:
  1. Edit .env and fill in real credentials (see .env.example for docs on
     each variable). Do not commit this file.
  2. Place your GCP service account JSON key somewhere on disk (outside
     this repo) and point GCS_CREDENTIALS_FILE at it.
  3. (Optional) Set up the nginx virtual host:
       sudo cp nginx/image-detection.conf.sample /etc/nginx/conf.d/image-detection.conf
       sudo $EDITOR /etc/nginx/conf.d/image-detection.conf   # set your real domain
       sudo nginx -t && sudo systemctl reload nginx
  4. Run the app:
       ./bin/image-detection
  5. Verify:
       curl http://127.0.0.1:8080/healthz
       curl -H "Authorization: Bearer $API_BEARER_TOKEN" \
            -F "file=@/path/to/test.jpg" \
            http://127.0.0.1:8080/api/process

For a production deployment, run the binary under a process supervisor
(systemd unit, supervisord, etc.) rather than invoking it directly — see
README.md's "Deploying to a fresh environment" section.
EOF
