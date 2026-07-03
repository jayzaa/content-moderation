// Package processhandler implements the end-to-end PoC pipeline:
//
//  1. accept a multipart image or video upload
//  2. for images: resize if needed to satisfy the moderation API's limits
//  3. store it temporarily in a private GCS bucket
//  4. generate a short-lived V4 signed URL for that object
//  5. call Alibaba Cloud content moderation (Green) on the signed URL —
//     synchronously for images, or submit+poll for videos (async API)
//  6. delete the GCS object regardless of moderation outcome
//  7. return both the raw moderation JSON and a human-readable summary
package processhandler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	green20220302 "github.com/alibabacloud-go/green-20220302/v2/client"

	"image-detection/internal/gcstemp"
	"image-detection/internal/imageproc"
	"image-detection/internal/moderation"
	"image-detection/internal/modresult"
	"image-detection/internal/reqlog"
)

// MaxImageUploadBytes caps accepted image uploads.
const MaxImageUploadBytes = 64 * 1024 * 1024 // 64 MB

// MaxVideoUploadBytes caps accepted video uploads, matching Alibaba
// Cloud's async video moderation limit (200 MB).
// See: https://help.aliyun.com/en/document_detail/176385.html
const MaxVideoUploadBytes = 200 * 1024 * 1024 // 200 MB

// VideoPollInterval and VideoPollTimeout control how the handler waits for
// an asynchronous video moderation task to finish before giving up.
const (
	VideoPollInterval = 3 * time.Second
	VideoPollTimeout  = 5 * time.Minute
)

// Handler serves the combined upload+moderate+cleanup pipeline.
type Handler struct {
	GCS        *gcstemp.Store
	ModClient  *green20220302.Client
	Logger     *reqlog.Logger // optional; nil disables call logging
	RequestTTL time.Duration  // overall timeout for image requests
}

// New creates a processhandler.Handler.
func New(gcs *gcstemp.Store, modClient *green20220302.Client, logger *reqlog.Logger) *Handler {
	return &Handler{GCS: gcs, ModClient: modClient, Logger: logger, RequestTTL: 60 * time.Second}
}

type processResponse struct {
	Kind    string      `json:"kind"` // "image" or "video"
	Resized bool        `json:"resized,omitempty"`
	Raw     interface{} `json:"raw"`
	Summary interface{} `json:"summary"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// ServeHTTP handles POST multipart/form-data requests with a single "file"
// field containing the image or video to moderate.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.GCS == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend not configured")
		return
	}
	if h.ModClient == nil {
		writeError(w, http.StatusServiceUnavailable, "content moderation is not configured (missing Alibaba Cloud credentials)")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxVideoUploadBytes)
	if err := r.ParseMultipartForm(MaxVideoUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart form: %v", err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing \"file\" field")
		return
	}
	defer file.Close()

	data := make([]byte, 0, header.Size)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if rerr != nil {
			break
		}
		if int64(len(data)) > MaxVideoUploadBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
	}

	contentType := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(contentType, "image/"):
		h.handleImage(w, r, data, header.Filename)
	case strings.HasPrefix(contentType, "video/"):
		h.handleVideo(w, r, data, header.Filename)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported or unrecognized file format (expected an image or video)")
	}
}

func (h *Handler) handleImage(w http.ResponseWriter, r *http.Request, data []byte, filename string) {
	if int64(len(data)) > MaxImageUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "image file too large")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.RequestTTL)
	defer cancel()

	processed, contentType, resized, err := imageproc.EnsureWithinLimits(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("could not process image: %v", err))
		return
	}

	objectName, err := randomObjectName(filename, ".jpg")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate object name")
		return
	}

	signedURL, err := h.GCS.Upload(ctx, objectName, processed, contentType)
	if err != nil {
		log.Printf("processhandler: gcs upload %s: %v", objectName, err)
		writeError(w, http.StatusInternalServerError, "failed to store file temporarily")
		return
	}
	defer h.cleanup(objectName)

	modResp, err := moderation.ModerateImageURL(h.ModClient, signedURL)
	if err != nil {
		msg := fmt.Sprintf("moderation request failed: %v", err)
		h.logCall("image", filename, "error", nil, nil, msg)
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	var raw interface{}
	var data2 *green20220302.ImageModerationResponseBodyData
	if modResp != nil && modResp.Body != nil {
		raw = modResp.Body
		data2 = modResp.Body.Data
	}

	summary := modresult.Summarize(data2)
	h.logCall("image", filename, "ok", summary, raw, "")

	resp := processResponse{
		Kind:    "image",
		Resized: resized,
		Raw:     raw,
		Summary: summary,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleVideo(w http.ResponseWriter, r *http.Request, data []byte, filename string) {
	if int64(len(data)) > MaxVideoUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "video file too large (max 200MB)")
		return
	}

	// Video moderation is asynchronous and can take well beyond typical
	// request timeouts, so this context is scoped to poll timeout + upload
	// time rather than the shorter image RequestTTL.
	ctx, cancel := context.WithTimeout(r.Context(), VideoPollTimeout+30*time.Second)
	defer cancel()

	contentType := http.DetectContentType(data)

	objectName, err := randomObjectName(filename, ".mp4")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate object name")
		return
	}

	signedURL, err := h.GCS.Upload(ctx, objectName, data, contentType)
	if err != nil {
		log.Printf("processhandler: gcs upload %s: %v", objectName, err)
		writeError(w, http.StatusInternalServerError, "failed to store file temporarily")
		return
	}
	// Always clean up the GCS object using a fresh background context so
	// that cleanup is never skipped even if the request context is cancelled
	// or the poll timeout fires. This is the fix for video files lingering
	// in the bucket after async moderation completes or errors out.
	defer func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()
		if err := h.GCS.Delete(cleanCtx, objectName); err != nil {
			log.Printf("processhandler: video gcs cleanup %s: %v", objectName, err)
		} else {
			log.Printf("processhandler: video gcs cleanup %s: deleted", objectName)
		}
	}()

	taskID, err := moderation.SubmitVideoURL(h.ModClient, signedURL)
	if err != nil {
		msg := fmt.Sprintf("video moderation submission failed: %v", err)
		h.logCall("video", filename, "error", nil, nil, msg)
		writeError(w, http.StatusBadGateway, msg)
		return
	}

	resultResp, err := h.pollVideoResult(ctx, taskID)
	if err != nil {
		msg := fmt.Sprintf("video moderation did not complete: %v", err)
		h.logCall("video", filename, "error", nil, nil, msg)
		writeError(w, http.StatusGatewayTimeout, msg)
		return
	}

	var raw interface{}
	var data2 *green20220302.VideoModerationResultResponseBodyData
	if resultResp != nil && resultResp.Body != nil {
		raw = resultResp.Body
		data2 = resultResp.Body.Data
	}

	summary := modresult.SummarizeVideo(data2)
	h.logCall("video", filename, "ok", summary, raw, "")

	resp := processResponse{
		Kind:    "video",
		Raw:     raw,
		Summary: summary,
	}
	writeJSON(w, http.StatusOK, resp)
}

// pollVideoResult repeatedly queries the async video moderation task until
// it completes or the context is done.
func (h *Handler) pollVideoResult(ctx context.Context, taskID string) (*green20220302.VideoModerationResultResponse, error) {
	ticker := time.NewTicker(VideoPollInterval)
	defer ticker.Stop()

	for {
		resp, err := moderation.PollVideoResult(h.ModClient, taskID)
		if err != nil {
			return nil, err
		}
		if modresult.VideoTaskComplete(resp) {
			return resp, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for task %s: %w", taskID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (h *Handler) cleanup(objectName string) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	if err := h.GCS.Delete(cleanupCtx, objectName); err != nil {
		log.Printf("processhandler: gcs cleanup %s: %v", objectName, err)
	}
}

// logCall persists a record of one API call outcome via h.Logger, if
// configured. Logging failures are non-fatal: they're written to the
// application log but never affect the actual API response.
func (h *Handler) logCall(kind, filename, status string, summary, raw interface{}, errMsg string) {
	if h.Logger == nil {
		return
	}
	entry := reqlog.Entry{
		Kind:     kind,
		Filename: filename,
		Status:   status,
		Summary:  summary,
		Raw:      raw,
		Error:    errMsg,
	}
	if err := h.Logger.Log(entry); err != nil {
		log.Printf("processhandler: reqlog: %v", err)
	}
}

func randomObjectName(original, fallbackExt string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(original))
	if ext == "" || len(ext) > 5 {
		ext = fallbackExt
	}
	return "tmp/" + hex.EncodeToString(buf) + ext, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
