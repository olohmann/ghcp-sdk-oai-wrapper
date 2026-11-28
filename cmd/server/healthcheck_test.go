package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthURL(t *testing.T) {
	if got := healthURL("8080", "openai"); got != "http://127.0.0.1:8080/healthz" {
		t.Errorf("openai health URL = %q", got)
	}
	if got := healthURL("11434", "ollama"); got != "http://127.0.0.1:11434/" {
		t.Errorf("ollama health URL = %q", got)
	}
}

func TestProbeHealth(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(200 * time.Millisecond)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if !probeHealth(srv.URL+"/healthz", time.Second) {
		t.Error("2xx response must be healthy")
	}
	status = http.StatusServiceUnavailable
	if probeHealth(srv.URL+"/healthz", time.Second) {
		t.Error("5xx response must be unhealthy")
	}
	status = http.StatusOK
	if probeHealth(srv.URL+"/slow", 50*time.Millisecond) {
		t.Error("timeout must be unhealthy")
	}
	closed := srv.URL
	srv.Close()
	if probeHealth(closed+"/healthz", time.Second) {
		t.Error("unreachable server must be unhealthy")
	}
}
