package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func post(url string, body interface{}) (int, string) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func get(url string) (int, string) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func main() {
	base := "http://localhost:8080"

	fmt.Println("=== Part 1: Rate Limiting ===")

	// Valid request
	code, body := post(base+"/request", map[string]interface{}{"user_id": "alice", "payload": map[string]int{"n": 1}})
	fmt.Printf("[POST /request] %d: %s\n", code, body)

	// Missing user_id
	code, body = post(base+"/request", map[string]interface{}{"payload": 42})
	fmt.Printf("[POST /request missing user_id] %d: %s\n", code, body)

	// Rate limit: 6 requests for bob
	fmt.Println("\nSending 6 requests for 'bob' (expect 5 accepted, 1 rejected):")
	for i := 1; i <= 6; i++ {
		code, body = post(base+"/request", map[string]interface{}{"user_id": "bob", "payload": i})
		fmt.Printf("  #%d -> %d: %s\n", i, code, body)
	}

	// Stats
	code, body = get(base + "/stats")
	fmt.Printf("\n[GET /stats] %d: %s\n", code, body)

	fmt.Println("\n=== Part 2: Product Catalog ===")

	// Create product
	code, body = post(base+"/products", map[string]interface{}{
		"name": "Widget A",
		"sku":  "SKU-001",
		"image_urls": []string{
			"https://cdn.example.com/products/sku-001/img-1.jpg",
			"https://cdn.example.com/products/sku-001/img-2.jpg",
		},
		"video_urls": []string{
			"https://cdn.example.com/products/sku-001/demo.mp4",
		},
	})
	fmt.Printf("[POST /products] %d: %s\n", code, body)

	// Duplicate SKU
	code, body = post(base+"/products", map[string]interface{}{"name": "Widget B", "sku": "SKU-001"})
	fmt.Printf("[POST /products duplicate SKU] %d: %s\n", code, body)

	// Second product
	post(base+"/products", map[string]interface{}{
		"name":       "Gadget Z",
		"sku":        "SKU-002",
		"image_urls": []string{"https://cdn.example.com/products/sku-002/img-1.jpg"},
		"video_urls": []string{},
	})

	// List products
	code, body = get(base + "/products?limit=10&offset=0")
	fmt.Printf("[GET /products] %d: %s\n", code, body)

	// Detail
	code, body = get(base + "/products/1")
	fmt.Printf("[GET /products/1] %d: %s\n", code, body)

	// Unknown id
	code, body = get(base + "/products/9999")
	fmt.Printf("[GET /products/9999] %d: %s\n", code, body)

	// Add media
	code, body = post(base+"/products/1/media", map[string]interface{}{
		"image_urls": []string{"https://cdn.example.com/products/sku-001/img-3.jpg"},
		"video_urls": []string{},
	})
	fmt.Printf("[POST /products/1/media] %d: %s\n", code, body)

	// Empty media body → 400
	code, body = post(base+"/products/1/media", map[string]interface{}{
		"image_urls": []string{},
		"video_urls": []string{},
	})
	fmt.Printf("[POST /products/1/media empty] %d: %s\n", code, body)

	// Invalid URL
	code, body = post(base+"/products", map[string]interface{}{
		"name":       "Bad",
		"sku":        "SKU-999",
		"image_urls": []string{"not-a-url"},
	})
	fmt.Printf("[POST /products invalid URL] %d: %s\n", code, body)

	// Search filter
	code, body = get(base + "/products?search=widget")
	fmt.Printf("[GET /products?search=widget] %d: %s\n", code, body)

	// Search no match
	code, body = get(base + "/products?search=nonexistent")
	fmt.Printf("[GET /products?search=nonexistent] %d: %s\n", code, body)
}
