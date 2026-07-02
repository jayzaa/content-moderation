// Package gcstemp manages temporary object storage in Google Cloud Storage
// for images awaiting content moderation.
//
// Objects are uploaded to a private bucket (no public access), accessed
// only via short-lived V4 signed URLs, and deleted once processing
// finishes (success or failure) so nothing lingers in the bucket.
package gcstemp

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// Config configures the GCS temp-storage client.
type Config struct {
	// ProjectID is the GCP project owning the bucket.
	ProjectID string
	// Bucket is the GCS bucket name used for temporary uploads.
	Bucket string
	// CredentialsFile is the path to a service account JSON key file.
	CredentialsFile string
	// SignedURLExpiry is how long a generated signed URL remains valid.
	// Keep this short since the URL grants read access to the object.
	SignedURLExpiry time.Duration
}

// Store wraps a GCS client scoped to a single bucket.
type Store struct {
	client  *storage.Client
	bucket  string
	saEmail string
	saKey   []byte
	expiry  time.Duration
}

// New creates a Store using the service account credentials file at
// cfg.CredentialsFile. The service account must have Storage Object Admin
// (or at least create/delete/get) permission on cfg.Bucket, and must be
// able to sign URLs (it needs its own private key, which a JSON key file
// provides).
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("gcstemp: Bucket is required")
	}
	if cfg.CredentialsFile == "" {
		return nil, fmt.Errorf("gcstemp: CredentialsFile is required")
	}

	keyData, err := os.ReadFile(cfg.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("gcstemp: read credentials file: %w", err)
	}

	saEmail, err := extractClientEmail(keyData)
	if err != nil {
		return nil, fmt.Errorf("gcstemp: parse credentials file: %w", err)
	}

	client, err := storage.NewClient(ctx, option.WithCredentialsJSON(keyData))
	if err != nil {
		return nil, fmt.Errorf("gcstemp: create storage client: %w", err)
	}

	expiry := cfg.SignedURLExpiry
	if expiry <= 0 {
		expiry = 10 * time.Minute
	}

	return &Store{
		client:  client,
		bucket:  cfg.Bucket,
		saEmail: saEmail,
		saKey:   keyData,
		expiry:  expiry,
	}, nil
}

// Close releases the underlying GCS client resources.
func (s *Store) Close() error {
	return s.client.Close()
}

// Upload writes data to a new object named objectName in the bucket and
// returns a V4 signed GET URL valid for the store's configured expiry.
func (s *Store) Upload(ctx context.Context, objectName string, data []byte, contentType string) (signedURL string, err error) {
	obj := s.client.Bucket(s.bucket).Object(objectName)

	w := obj.NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("gcstemp: write object: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("gcstemp: finalize object: %w", err)
	}

	url, err := s.SignedURL(objectName)
	if err != nil {
		// Best-effort cleanup if we can't hand back a usable URL.
		_ = obj.Delete(ctx)
		return "", err
	}
	return url, nil
}

// SignedURL generates a time-limited V4 signed GET URL for objectName.
func (s *Store) SignedURL(objectName string) (string, error) {
	opts := &storage.SignedURLOptions{
		Scheme:         storage.SigningSchemeV4,
		Method:         "GET",
		GoogleAccessID: s.saEmail,
		PrivateKey:     extractPrivateKeyPEM(s.saKey),
		Expires:        time.Now().Add(s.expiry),
	}
	url, err := s.client.Bucket(s.bucket).SignedURL(objectName, opts)
	if err != nil {
		return "", fmt.Errorf("gcstemp: sign URL: %w", err)
	}
	return url, nil
}

// Delete removes objectName from the bucket. Safe to call even if the
// object no longer exists is not guaranteed by GCS; callers that want
// idempotent cleanup should ignore storage.ErrObjectNotExist.
func (s *Store) Delete(ctx context.Context, objectName string) error {
	err := s.client.Bucket(s.bucket).Object(objectName).Delete(ctx)
	if err != nil && err != storage.ErrObjectNotExist {
		return fmt.Errorf("gcstemp: delete object: %w", err)
	}
	return nil
}

// Reader opens a reader for objectName (not currently used by the main
// upload flow, but useful for diagnostics/tests).
func (s *Store) Reader(ctx context.Context, objectName string) (io.ReadCloser, error) {
	return s.client.Bucket(s.bucket).Object(objectName).NewReader(ctx)
}
