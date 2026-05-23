package main

import (
	"log"
	"net/http"

	"sourceasia-backend/ratelimit"
	"sourceasia-backend/catalog"
)

func main() {
	mux := http.NewServeMux()

	// Part 1 – Rate-limited API
	rl := ratelimit.NewHandler()
	mux.HandleFunc("/request", rl.HandleRequest)
	mux.HandleFunc("/stats", rl.HandleStats)

	// Part 2 – Product catalog
	cat := catalog.NewHandler()
	mux.HandleFunc("/products", cat.HandleProducts)
	mux.HandleFunc("/products/", cat.HandleProductByID)

	addr := ":8080"
	log.Printf("Server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
