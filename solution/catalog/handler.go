package catalog

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Data model
// ---------------------------------------------------------------------------

// Product holds the core product fields.
type Product struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SKU       string    `json:"sku"`
	CreatedAt time.Time `json:"created_at"`
}

// Media holds all URL arrays separately from the product row so that the list
// endpoint never has to load them.
type Media struct {
	ImageURLs []string
	VideoURLs []string
}

// ListItem is the lightweight representation returned by GET /products.
type ListItem struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	SKU          string    `json:"sku"`
	ImageCount   int       `json:"image_count"`
	VideoCount   int       `json:"video_count"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// DetailItem is the full representation returned by GET /products/{id}.
type DetailItem struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SKU       string    `json:"sku"`
	ImageURLs []string  `json:"image_urls"`
	VideoURLs []string  `json:"video_urls"`
	CreatedAt time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// In-memory store
// ---------------------------------------------------------------------------

// store is the in-memory storage layer.
type store struct {
	mu           sync.RWMutex
	products     []Product         // ordered slice for pagination
	productIndex map[int64]int     // product_id → index in products slice (O(1) lookup)
	media        map[int64]*Media  // product_id → media
	skuIndex     map[string]int64  // sku → product_id  (uniqueness check)
	counter      atomic.Int64      // auto-increment id
}

func newStore() *store {
	return &store{
		productIndex: make(map[int64]int),
		media:        make(map[int64]*Media),
		skuIndex:     make(map[string]int64),
	}
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

const (
	maxURLLength  = 2048
	maxURLsPerReq = 20
)

func validateURL(raw string) bool {
	if len(raw) > maxURLLength {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func validateURLSlice(urls []string, fieldName string) string {
	if len(urls) > maxURLsPerReq {
		return fieldName + ": maximum 20 URLs per request"
	}
	for _, u := range urls {
		if !validateURL(u) {
			return fieldName + ": invalid URL \"" + u + "\" (must be http/https, max 2048 chars)"
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// HTTP Handler
// ---------------------------------------------------------------------------

// Handler implements the product catalog HTTP handlers.
type Handler struct {
	st *store
}

// NewHandler creates and returns a new catalog Handler.
func NewHandler() *Handler {
	return &Handler{st: newStore()}
}

// HandleProducts dispatches POST /products and GET /products.
func (h *Handler) HandleProducts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createProduct(w, r)
	case http.MethodGet:
		h.listProducts(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// HandleProductByID dispatches GET /products/{id} and POST /products/{id}/media.
func (h *Handler) HandleProductByID(w http.ResponseWriter, r *http.Request) {
	// Strip leading "/products/"
	path := strings.TrimPrefix(r.URL.Path, "/products/")

	// POST /products/{id}/media
	if strings.HasSuffix(path, "/media") {
		idStr := strings.TrimSuffix(path, "/media")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid product id"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		h.addMedia(w, r, id)
		return
	}

	// GET /products/{id}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid product id"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	h.getProduct(w, r, id)
}

// ---------------------------------------------------------------------------
// POST /products
// ---------------------------------------------------------------------------

func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return
	}
	var body struct {
		Name      string   `json:"name"`
		SKU       string   `json:"sku"`
		ImageURLs []string `json:"image_urls"`
		VideoURLs []string `json:"video_urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required and must be non-empty"})
		return
	}
	if strings.TrimSpace(body.SKU) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sku is required and must be non-empty"})
		return
	}
	if msg := validateURLSlice(body.ImageURLs, "image_urls"); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	if msg := validateURLSlice(body.VideoURLs, "video_urls"); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	h.st.mu.Lock()
	defer h.st.mu.Unlock()

	if _, exists := h.st.skuIndex[body.SKU]; exists {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "duplicate sku"})
		return
	}

	id := h.st.counter.Add(1)
	p := Product{ID: id, Name: body.Name, SKU: body.SKU, CreatedAt: time.Now()}
	h.st.productIndex[id] = len(h.st.products) // record insertion index before appending
	h.st.products = append(h.st.products, p)
	h.st.skuIndex[body.SKU] = id

	imgs := make([]string, len(body.ImageURLs))
	copy(imgs, body.ImageURLs)
	vids := make([]string, len(body.VideoURLs))
	copy(vids, body.VideoURLs)
	h.st.media[id] = &Media{ImageURLs: imgs, VideoURLs: vids}

	thumb := ""
	if len(imgs) > 0 {
		thumb = imgs[0]
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":            id,
		"name":          p.Name,
		"sku":           p.SKU,
		"image_urls":    imgs,
		"video_urls":    vids,
		"thumbnail_url": thumb,
		"created_at":    p.CreatedAt,
	})
}

// ---------------------------------------------------------------------------
// GET /products  (list – no media arrays)
// ---------------------------------------------------------------------------

const (
	defaultLimit = 20
	maxLimit     = 100
)

func (h *Handler) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := defaultLimit
	if lStr := q.Get("limit"); lStr != "" {
		v, err := strconv.Atoi(lStr)
		if err != nil || v < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		if v > maxLimit {
			v = maxLimit
		}
		limit = v
	}

	offset := 0
	if oStr := q.Get("offset"); oStr != "" {
		v, err := strconv.Atoi(oStr)
		if err != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset must be a non-negative integer"})
			return
		}
		offset = v
	}

	// Optional search filter (case-insensitive substring match)
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))

	h.st.mu.RLock()

	// Build the filtered view first (still only reads lightweight Product structs)
	var filtered []Product
	if search == "" {
		filtered = h.st.products
	} else {
		for _, p := range h.st.products {
			if strings.Contains(strings.ToLower(p.Name), search) ||
				strings.Contains(strings.ToLower(p.SKU), search) {
				filtered = append(filtered, p)
			}
		}
	}

	total := len(filtered)
	end := offset + limit
	if end > total {
		end = total
	}
	var page []Product
	if offset < total {
		page = filtered[offset:end]
	}

	items := make([]ListItem, 0, len(page))
	for _, p := range page {
		m := h.st.media[p.ID]
		thumb := ""
		imgCount, vidCount := 0, 0
		if m != nil {
			imgCount = len(m.ImageURLs)
			vidCount = len(m.VideoURLs)
			if imgCount > 0 {
				thumb = m.ImageURLs[0]
			}
		}
		items = append(items, ListItem{
			ID:           p.ID,
			Name:         p.Name,
			SKU:          p.SKU,
			ImageCount:   imgCount,
			VideoCount:   vidCount,
			ThumbnailURL: thumb,
			CreatedAt:    p.CreatedAt,
		})
	}
	h.st.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"items":  items,
	})
}

// ---------------------------------------------------------------------------
// GET /products/{id}  (detail – full media arrays)
// ---------------------------------------------------------------------------

func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request, id int64) {
	h.st.mu.RLock()
	defer h.st.mu.RUnlock()

	// O(1) lookup via productIndex map instead of O(n) linear scan
	idx, ok := h.st.productIndex[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found"})
		return
	}
	found := &h.st.products[idx]

	m := h.st.media[id]
	imgs, vids := []string{}, []string{}
	if m != nil {
		imgs = m.ImageURLs
		vids = m.VideoURLs
	}

	writeJSON(w, http.StatusOK, DetailItem{
		ID:        found.ID,
		Name:      found.Name,
		SKU:       found.SKU,
		ImageURLs: imgs,
		VideoURLs: vids,
		CreatedAt: found.CreatedAt,
	})
}

// ---------------------------------------------------------------------------
// POST /products/{id}/media
// ---------------------------------------------------------------------------

func (h *Handler) addMedia(w http.ResponseWriter, r *http.Request, id int64) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return
	}
	var body struct {
		ImageURLs []string `json:"image_urls"`
		VideoURLs []string `json:"video_urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if len(body.ImageURLs) == 0 && len(body.VideoURLs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one of image_urls or video_urls is required"})
		return
	}
	if msg := validateURLSlice(body.ImageURLs, "image_urls"); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	if msg := validateURLSlice(body.VideoURLs, "video_urls"); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	h.st.mu.Lock()
	defer h.st.mu.Unlock()

	// O(1) existence check via productIndex map
	if _, ok := h.st.productIndex[id]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found"})
		return
	}

	m := h.st.media[id]
	if m == nil {
		m = &Media{}
		h.st.media[id] = m
	}
	m.ImageURLs = append(m.ImageURLs, body.ImageURLs...)
	m.VideoURLs = append(m.VideoURLs, body.VideoURLs...)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"product_id":  id,
		"image_count": len(m.ImageURLs),
		"video_count": len(m.VideoURLs),
	})
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
