package main

import (
	"context"
	"net/http"
	"time"
)

// healthURL returns the local endpoint that indicates readiness for mode.
func healthURL(port, mode string) string {
	path := "/healthz"
	if mode == "ollama" {
		path = "/"
	}
	return "http://127.0.0.1:" + port + path
}

// probeHealth reports whether url answers with a 2xx status within timeout.
// The image has no shell or HTTP client, so the container health check runs
// this through `/server --healthcheck`.
func probeHealth(url string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
