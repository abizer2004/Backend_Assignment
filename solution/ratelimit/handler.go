package ratelimit

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// windowEntry tracks request timestamps in a rolling 1-minute window per user.
type windowEntry struct {
	mu           sync.Mutex
	timestamps   []time.Time // accepted request timestamps
	rejectedTotal int64      // cumulative rejected count (all time)
}

// Handler holds the in-memory state for the rate-limited API.
type Handler struct {
	mu    sync.Mutex
	users map[string]*windowEntry
}

// NewHandler creates and returns a new rate-limit Handler.
func NewHandler() *Handler {
	return &Handler{users: make(map[string]*windowEntry)}
}

const (
	maxRequests = 5
	window      = time.Minute
)

// getOrCreate returns the windowEntry for a user, creating it if necessary.
func (h *Handler) getOrCreate(userID string) *windowEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.users[userID]; ok {
		return e
	}
	e := &windowEntry{}
	h.users[userID] = e
	return e
}

// prune removes timestamps outside the current rolling window.
// Must be called with e.mu held.
func prune(e *windowEntry) {
	cutoff := time.Now().Add(-window)
	i := 0
	for i < len(e.timestamps) && e.timestamps[i].Before(cutoff) {
		i++
	}
	e.timestamps = e.timestamps[i:]
}

// HandleRequest handles POST /request
func (h *Handler) HandleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return
	}

	var body struct {
		UserID  string      `json:"user_id"`
		Payload interface{} `json:"payload"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required and must be non-empty"})
		return
	}
	if body.Payload == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload is required"})
		return
	}

	entry := h.getOrCreate(body.UserID)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	prune(entry)

	if len(entry.timestamps) >= maxRequests {
		entry.rejectedTotal++
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error":   "rate limit exceeded",
			"message": "maximum 5 requests per 1-minute rolling window",
		})
		return
	}

	entry.timestamps = append(entry.timestamps, time.Now())
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":  "accepted",
		"user_id": body.UserID,
	})
}

// statsUser is the per-user stats object returned by GET /stats.
type statsUser struct {
	AcceptedInWindow int   `json:"accepted_in_current_window"`
	RejectedTotal    int64 `json:"rejected_total"`
}

// HandleStats handles GET /stats
func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	h.mu.Lock()
	// snapshot user list so we can release the top-level lock quickly
	users := make(map[string]*windowEntry, len(h.users))
	for k, v := range h.users {
		users[k] = v
	}
	h.mu.Unlock()

	result := make(map[string]statsUser, len(users))
	globalAccepted := 0
	var globalRejected int64

	for id, e := range users {
		e.mu.Lock()
		prune(e)
		accepted := len(e.timestamps)
		rejected := e.rejectedTotal
		e.mu.Unlock()

		result[id] = statsUser{AcceptedInWindow: accepted, RejectedTotal: rejected}
		globalAccepted += accepted
		globalRejected += rejected
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"users": result,
		"global": map[string]interface{}{
			"accepted_in_current_window": globalAccepted,
			"rejected_total":             globalRejected,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
