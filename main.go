// Command image-detection is the entrypoint for the image-detection service.
//
// Serves the upload/moderation PoC UI (htdocs/) and the API endpoints used
// by the UI:
//
//	POST /api/process   accept a multipart image or video, resize images if
//	                     needed, upload the file temporarily to Google
//	                     Cloud Storage, generate a signed URL, run Alibaba
//	                     Cloud content moderation on it (synchronous for
//	                     images, submit+poll for videos), delete the GCS
//	                     object, and return the result.
//	GET  /api/logs[/id]  list recent call results, or fetch one in full.
//
// All configuration (GCS bucket/project, credential paths, Alibaba
// region/keys, the API bearer token) comes from environment variables,
// loaded from a local .env file if present (see .env.example). Nothing
// environment-specific is hard-coded in source.
//
// /api/* requires "Authorization: Bearer <API_BEARER_TOKEN>".
//
// The Go HTTP server binds 127.0.0.1:8080 by default (see LISTEN_ADDR);
// nginx is expected to reverse-proxy to it (see nginx/ for a sample vhost).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"image-detection/internal/auth"
	"image-detection/internal/config"
	"image-detection/internal/gcstemp"
	"image-detection/internal/moderation"
	"image-detection/internal/processhandler"
	"image-detection/internal/reqlog"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	// Load .env (if present) before reading config; real env vars always
	// take precedence over values from the file.
	if err := config.LoadDotEnv(filepath.Join(root, ".env")); err != nil {
		log.Fatalf("load .env: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("%v", err)
	}

	htdocsDir := filepath.Join(root, "htdocs")

	ctx := context.Background()
	gcs, err := gcstemp.New(ctx, gcstemp.Config{
		ProjectID:       cfg.GCPProjectID,
		Bucket:          cfg.GCSBucket,
		CredentialsFile: cfg.GCSCredentialsFile,
		SignedURLExpiry: cfg.SignedURLExpiry,
	})
	if err != nil {
		log.Fatalf("configure GCS storage: %v", err)
	}
	defer gcs.Close()

	modClient, err := moderation.NewClient(cfg.AlibabaAccessKeyID, cfg.AlibabaAccessKeySecret, cfg.AlibabaRegionID)
	if err != nil {
		log.Fatalf("configure content moderation client: %v", err)
	}

	logsDir := cfg.LogsDir
	if !filepath.IsAbs(logsDir) {
		logsDir = filepath.Join(root, logsDir)
	}
	logger, err := reqlog.New(logsDir)
	if err != nil {
		log.Fatalf("configure request logger: %v", err)
	}

	process := processhandler.New(gcs, modClient, logger)
	logsHandler := reqlog.NewHandler(logger)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})

	// All /api/* endpoints require the bearer token.
	mux.Handle("/api/process", auth.BearerMiddleware(cfg.APIBearerToken, http.HandlerFunc(process.ServeHTTP)))
	mux.Handle("/api/logs", auth.BearerMiddleware(cfg.APIBearerToken, logsHandler))
	mux.Handle("/api/logs/", auth.BearerMiddleware(cfg.APIBearerToken, logsHandler))

	// Serve the PoC UI (index.html, css, js) from htdocs/, at the site root.
	mux.Handle("/", http.FileServer(http.Dir(htdocsDir)))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute, // allow large video uploads
		WriteTimeout:      6 * time.Minute, // allow for async video moderation polling
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		log.Printf("image-detection listening on http://%s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("stopped")
}
