package reqlog

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Handler serves the log index and individual log entries over HTTP for
// the htdocs viewer page.
//
//	GET /api/logs           -> JSON array of recent call summaries
//	GET /api/logs/<file>     -> full JSON record for one call
type Handler struct {
	Logger *Logger
}

// NewHandler creates a log-viewing Handler.
func NewHandler(logger *Logger) *Handler {
	return &Handler{Logger: logger}
}

// ServeHTTP implements http.Handler. It expects to be mounted at
// "/api/logs" (or "/api/logs/") with the remainder of the path (if any)
// treated as a specific log filename to fetch.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/logs")
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		h.serveList(w)
		return
	}
	h.serveEntry(w, rest)
}

func (h *Handler) serveList(w http.ResponseWriter) {
	records, err := h.Logger.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(records)
}

func (h *Handler) serveEntry(w http.ResponseWriter, filename string) {
	data, err := h.Logger.Get(filename)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "log entry not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
