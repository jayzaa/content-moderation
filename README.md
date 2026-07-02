# image-detection

A small Go service that runs uploaded images and videos through content
moderation: it stores the file temporarily in Google Cloud Storage,
generates a short-lived signed URL, sends that URL to Alibaba Cloud
Content Moderation (Green/CIP API), deletes the temporary file, and
returns both the raw API response and a human-readable summary.

This is a proof of concept — see [Security notes](#security-notes) before
using it for anything beyond local testing/demoing.

## Table of contents

- [Stack](#stack)
- [Pipeline flow](#pipeline-flow)
- [Project layout](#project-layout)
- [Configuration](#configuration)
- [Building](#building)
- [Running locally](#running-locally)
- [Quick setup with install.sh](#quick-setup-with-installsh)
- [Running with Docker](#running-with-docker)
- [Deploying to a fresh environment](#deploying-to-a-fresh-environment)
- [API manual](#api-manual)
- [Security notes](#security-notes)

## Stack

| Layer | Choice | Why |
|---|---|---|
| Language / runtime | Go 1.24, standard `net/http` | No web framework needed for ~3 endpoints; stdlib has everything (timeouts, multipart parsing, routing via `http.ServeMux`). |
| Temporary file storage | Google Cloud Storage, private bucket, V4 signed URLs | Alibaba's moderation API needs a fetchable URL (or Alibaba OSS). We use our own GCS bucket instead of Alibaba OSS, so the temp file never touches Alibaba's storage — just a time-limited signed URL. Deleted immediately after moderation completes. |
| Content moderation | Alibaba Cloud Content Moderation (Green/CIP), API version `2022-03-02` | Image: `postImageCheckByVL_global` (large+small model hybrid, synchronous). Video: `videoDetection_global` (async: submit task, poll for result). |
| Image resizing | `golang.org/x/image/draw` | Stdlib `image` package can decode/encode but has no resize; resizing is needed to keep uploads within the moderation API's limits (20MB / 30,000px / 250M pixels). |
| Auth | Single static bearer token, checked with constant-time comparison | Explicitly requested as "easy mode" — no per-client tokens, no rotation. Not a full auth system. |
| Reverse proxy | nginx (sample config provided, not required) | TLS termination, real domain, and body-size/timeout tuning belong in front of the app, not in it. |
| Frontend | Static HTML/CSS/vanilla JS, served directly by the Go app from `htdocs/` | No build step, no framework — this is a PoC UI for exercising the API and viewing logs. |

## Pipeline flow

```
                    ┌─────────────────────────┐
                    │   Browser (htdocs UI)   │
                    │  or any HTTP client     │
                    └────────────┬────────────┘
                                 │ POST /api/process
                                 │ Authorization: Bearer <token>
                                 │ multipart/form-data, field "file"
                                 ▼
                    ┌─────────────────────────┐
                    │   nginx (optional)      │  TLS, domain, timeouts
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │   Go app (main.go)      │
                    │   127.0.0.1:8080        │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │ auth.BearerMiddleware   │  reject if token missing/wrong
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │ processhandler          │
                    │  1. detect content-type │  image/* or video/*
                    │  2. (image) resize if   │
                    │     over API limits     │
                    └────────────┬────────────┘
                                 ▼
                ┌────────────────────────────────┐
                │ gcstemp.Store.Upload            │
                │  → private GCS bucket           │
                │  → V4 signed URL (short-lived)  │
                └────────────────┬────────────────┘
                                 ▼
        ┌────────────────────────┴─────────────────────────┐
        ▼ image: synchronous                                ▼ video: async submit + poll
┌───────────────────────┐                      ┌──────────────────────────────┐
│ moderation             │                      │ moderation                   │
│ .ModerateImageURL      │                      │ .SubmitVideoURL (get taskId) │
│  → postImageCheckByVL  │                      │ .PollVideoResult (loop until │
│    _global             │                      │    riskLevel is populated,   │
└───────────┬────────────┘                      │    or 5 min timeout)         │
            │                                    │  → videoDetection_global    │
            │                                    └──────────────┬───────────────┘
            └────────────────────┬──────────────────────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │ gcstemp.Store.Delete     │  always runs (success or failure)
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │ modresult.Summarize(...) │  raw JSON → human-readable summary
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │ reqlog.Logger.Log        │  persist to logs/, update index.json
                    └────────────┬────────────┘
                                 ▼
                    ┌─────────────────────────┐
                    │ JSON response to client │  { kind, raw, summary, resized }
                    └─────────────────────────┘
```

## Project layout

```
image-detection/
  main.go                             entrypoint; wires everything together
  internal/
    config/                           env var + .env loading
    auth/                             bearer token middleware
    gcstemp/                         GCS temp storage (upload, signed URL, delete)
    moderation/                       Alibaba Green/CIP API client (image + video)
    modresult/                        raw API response → human-readable summary
    imageproc/                        resize images to satisfy moderation limits
    processhandler/                   the /api/process pipeline handler
    reqlog/                          persists call results, serves /api/logs
  htdocs/                             static frontend (served by the Go app)
  nginx/image-detection.conf.sample   sample reverse-proxy config
  install.sh                          fresh Debian 13 setup script
  Dockerfile                          multi-stage build for a container image
  docker-compose.yml                  container run config (env, volumes)
  .env.example                        documents every config variable
  logs/                               runtime-generated call logs (gitignored)
  bin/                                compiled binary output (gitignored)
  secrets/                            local-only credential mount point for Docker (gitignored)
```

## Configuration

All configuration is environment-variable driven — nothing environment-
specific (bucket names, project IDs, regions, credential paths, tokens) is
hard-coded in source. Copy `.env.example` to `.env` and fill in real
values:

```bash
cp .env.example .env
```

See `.env.example` for the full list with explanations. Summary:

| Variable | Required | Purpose |
|---|---|---|
| `LISTEN_ADDR` | no (default `127.0.0.1:8080`) | address the Go server binds to |
| `API_BEARER_TOKEN` | yes | single token required on all `/api/*` requests |
| `GCP_PROJECT_ID` | yes | GCP project owning the GCS bucket |
| `GCS_BUCKET` | yes | GCS bucket for temporary uploads |
| `GCS_CREDENTIALS_FILE` | yes | path to a GCP service account JSON key |
| `SIGNED_URL_EXPIRY_MINUTES` | no (default `10`) | how long signed URLs stay valid |
| `ALIBABA_CLOUD_ACCESS_KEY_ID` | yes | Alibaba Cloud RAM user AccessKey ID |
| `ALIBABA_CLOUD_ACCESS_KEY_SECRET` | yes | Alibaba Cloud RAM user AccessKey secret |
| `ALIBABA_CLOUD_REGION_ID` | no (default `ap-southeast-1`) | Alibaba Green/CIP region |
| `LOGS_DIR` | no (default `logs`) | where call logs are written |

The app loads `.env` automatically at startup if present. Real
environment variables (e.g. set by systemd, a container runtime, or your
shell) always take precedence over `.env` file values.

**Required external resources** (not provisioned by this repo):

- A GCP project with a GCS bucket and a service account that has
  create/read/delete permission on that bucket, plus permission to sign
  URLs (any service account key file works for this).
- An Alibaba Cloud account with Content Moderation (Green/CIP) activated,
  and a RAM user AccessKey with the `AliyunYundunGreenWebFullAccess`
  policy. Video moderation specifically requires the video moderation
  product to be activated separately from image moderation — if you get
  `commodityCode is invalid:...`, that's what's missing.

## Building

Requires Go 1.24+ (`go version` to check).

```bash
go mod download        # fetch dependencies
go build -o bin/image-detection .
go vet ./...            # static checks
gofmt -l .               # formatting check (no output = clean)
```

### Archiving a release build

```bash
VERSION=$(git rev-parse --short HEAD 2>/dev/null || echo "dev")
mkdir -p dist
go build -o dist/image-detection .
tar -czf dist/image-detection-${VERSION}.tar.gz \
  -C dist image-detection \
  -C .. htdocs .env.example nginx
```

This produces a tarball containing the compiled binary, the static
frontend, `.env.example` (as a template — never bundle a real `.env`),
and the sample nginx config. Deploy by extracting it and following
[Deploying to a fresh environment](#deploying-to-a-fresh-environment) from
step 3 onward.

## Running locally

```bash
cp .env.example .env    # then fill in real values
go run .
```

The server listens on `LISTEN_ADDR` (default `127.0.0.1:8080`). Visit
`http://127.0.0.1:8080/` for the UI, or call the API directly (see
[API manual](#api-manual)).

`go run .` compiles and runs directly from source in one step — no
separate `go build` needed for local iteration. Use the compiled binary
(`go build -o bin/image-detection .` then `./bin/image-detection`) for
anything beyond ad hoc local testing.

## Quick setup with install.sh

For a fresh Debian 13 host, `install.sh` automates the toolchain setup:

```bash
git clone <this-repo-url> image-detection
cd image-detection
./install.sh
```

This installs the Go toolchain and nginx (via `apt`, skipping either if
already present), downloads Go module dependencies, builds
`bin/image-detection`, and copies `.env.example` to `.env` if `.env`
doesn't already exist. It does **not** fill in any credentials or start
the app — you still need to edit `.env` and (optionally) set up the nginx
vhost yourself; the script prints the exact next steps when it finishes.

## Running with Docker

A multi-stage `Dockerfile` and `docker-compose.yml` are included as an
alternative to a bare-metal install.

```bash
cp .env.example .env             # fill in real values
mkdir -p secrets
cp /path/to/your-gcp-key.json secrets/gcs-service-account.json
docker compose up -d --build
```

Notes on the compose setup:

- `.env` is passed to the container via `env_file`. `LISTEN_ADDR` is
  overridden to `0.0.0.0:8080` inside the container regardless of what's
  in `.env`, since the container needs to bind all interfaces for the
  `8080:8080` port mapping to work.
- The GCP service account key is bind-mounted read-only from
  `./secrets/gcs-service-account.json` by default; override the host
  path with the `GCS_CREDENTIALS_FILE_HOST` environment variable if your
  key lives elsewhere. `GCS_CREDENTIALS_FILE` inside `.env` is overridden
  to the in-container mount path (`/run/secrets/gcs-service-account.json`)
  automatically.
- `logs/` is bind-mounted so request logs persist across container
  restarts/rebuilds. The container runs as UID 1000 (the common
  first-user UID on most Linux distros) specifically so this bind mount
  works read/write without extra permission tweaking on typical hosts —
  if your host user has a different UID, either `chown -R 1000:1000 logs/`
  or add a `user: "<uid>:<gid>"` override in `docker-compose.yml`.
- Verify with the same `curl` commands as [Deploying to a fresh
  environment](#deploying-to-a-fresh-environment), step 8.

```bash
docker compose logs -f       # tail app logs
docker compose down          # stop and remove the container
```

## Deploying to a fresh environment

These are the exact steps for a brand-new machine that has nothing
installed yet.

1. **Install the Go toolchain:**
   ```bash
   sudo apt update && sudo apt install -y golang-go
   go version   # verify
   ```

2. **Install nginx** (if you want a reverse proxy in front of the app —
   optional but recommended for TLS/domain handling):
   ```bash
   sudo apt install -y nginx
   ```

3. **Get the code onto the machine** (clone the repo, or extract a
   release tarball built per [Building](#building)):
   ```bash
   git clone <this-repo-url> image-detection
   cd image-detection
   ```

4. **Build the binary:**
   ```bash
   go build -o bin/image-detection .
   ```

5. **Set up the nginx virtual host:**
   ```bash
   sudo cp nginx/image-detection.conf.sample /etc/nginx/conf.d/image-detection.conf
   sudo $EDITOR /etc/nginx/conf.d/image-detection.conf   # replace the placeholder domain
   sudo nginx -t && sudo systemctl reload nginx
   ```

6. **Add credentials and configuration:**
   ```bash
   cp .env.example .env
   $EDITOR .env
   ```
   Fill in `API_BEARER_TOKEN` (e.g. `openssl rand -hex 32`),
   `GCP_PROJECT_ID`, `GCS_BUCKET`, `GCS_CREDENTIALS_FILE` (place the GCP
   service account JSON key somewhere on disk and point to it — do not
   put it inside the repo directory), and the `ALIBABA_CLOUD_*` values.

7. **Run the app.** For a quick test:
   ```bash
   ./bin/image-detection
   ```
   For a real deployment, run it under a process supervisor (systemd unit,
   supervisord, etc.) so it restarts on crash/reboot — this repo does not
   include a systemd unit file; add one appropriate to your environment,
   pointing `WorkingDirectory` at the repo root (so `.env` and `htdocs/`
   resolve correctly) and `ExecStart` at the compiled binary.

8. **Verify:**
   ```bash
   curl http://127.0.0.1:8080/healthz          # should return "ok"
   curl -H "Authorization: Bearer $TOKEN" \
        -F "file=@/path/to/test.jpg" \
        http://127.0.0.1:8080/api/process
   ```

That's it — no database, no message queue, no other services to stand up.

## API manual

All `/api/*` endpoints require:

```
Authorization: Bearer <API_BEARER_TOKEN>
```

Missing or wrong tokens get `401 Unauthorized` with a JSON error body.

### `GET /healthz`

No auth required. Returns `200 ok` (plain text) if the server is up.

### `POST /api/process`

Runs the full pipeline on one uploaded file: upload to GCS → signed URL →
Alibaba moderation → delete from GCS → return result. Also logs the
result (see `/api/logs`).

**Request:** `multipart/form-data` with a single field named `file`
containing an image or video.

- Images: any format Go's standard `image` package can decode (JPEG,
  PNG, GIF). Automatically resized if it exceeds Alibaba's limits (20MB,
  30,000px per side, 250M total pixels).
- Videos: any format `http.DetectContentType` recognizes as `video/*`.
  Capped at 200MB (Alibaba's async video moderation limit). Processing
  is asynchronous internally — expect this call to take anywhere from a
  few seconds to several minutes depending on video length; the server
  holds the HTTP request open and polls Alibaba until the result is
  ready (or up to 5 minutes, after which it returns a timeout error).

**Response (200):**

```json
{
  "kind": "image",
  "resized": false,
  "raw": { "...": "the full Alibaba Cloud API response body" },
  "summary": {
    "passed": false,
    "riskLevel": "high",
    "labels": [
      { "label": "violent_bloody", "riskLevel": "high", "confidence": 90.27, "description": "..." }
    ],
    "message": "Potential content risk(s) detected: see labels."
  }
}
```

For video, `summary` has `frameLabels` and `audioLabels` instead of
`labels`, since video moderation reports visual and audio findings
separately.

**Error responses:** `4xx`/`5xx` with `{"error": "description"}`. Notable
cases:

- `401` — missing/invalid bearer token
- `413` — file exceeds the size limit
- `415` — file isn't a recognized image or video
- `502` — the Alibaba Cloud API call itself failed (see the error message
  for the specific reason, e.g. an unactivated product/commodity)
- `504` — video moderation didn't complete within the poll timeout

### `GET /api/logs`

Returns a JSON array of recent call summaries (most recent first, capped
at 200 entries), for the frontend's log viewer:

```json
[
  {
    "id": "9962e53d53189ecf",
    "timestamp": "2026-07-02T13:18:35.654384516Z",
    "kind": "image",
    "filename": "example.jpg",
    "status": "ok",
    "file": "20260702T131835Z_9962e53d53189ecf.json"
  }
]
```

### `GET /api/logs/<file>`

Fetches the full logged record (raw response + summary + timestamp) for
one call, using the `file` value from the `/api/logs` list.

## Security notes

This is a proof of concept. Before using it for anything beyond local
testing:

- **The bearer token is a single shared secret.** Anyone with it has full
  access to the API (and burns your Alibaba Cloud / GCS quota). There's
  no per-client scoping, rate limiting, or rotation. Treat it like a
  password and rotate it if it leaks.
- **No rate limiting.** A malicious or buggy client can drive up your
  Alibaba Cloud and GCS bills quickly. Consider adding rate limiting
  (e.g. at the nginx layer) before exposing this publicly.
- **Signed URLs are time-limited but not otherwise restricted.** Anyone
  who obtains a signed URL before it expires can fetch that object. Keep
  `SIGNED_URL_EXPIRY_MINUTES` short.
- **Logs contain raw moderation results**, which may include sensitive
  content descriptions. `/api/logs` is behind the same bearer token as
  everything else, but there's no separate access control on log
  contents.
- **The frontend stores the bearer token in the browser's
  `sessionStorage`**, cleared when the tab closes. It is never written
  to disk or embedded in any served file.
