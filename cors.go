package main

import (
	"net/http"
	"os"
	"strings"
)

// withCORS wraps an http.Handler and adds CORS headers so the
// Next.js frontend can call this API from a different origin.
//
// By default it allows http://localhost:3000, but you can override it by
// setting CORS_ALLOWED_ORIGIN (e.g. "*" or "https://yourdomain.com").
func withCORS(next http.Handler) http.Handler {
	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:3000"
	}
	// If someone sets CORS_ALLOWED_ORIGIN to "localhost:3000" without scheme,
	// normalize it so the browser sees an exact match to Origin.
	if allowedOrigin != "*" && !strings.Contains(allowedOrigin, "://") {
		allowedOrigin = "http://" + allowedOrigin
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-User-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		// Handle preflight requests quickly
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
