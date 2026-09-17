package ui

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/yithcai/flow/internal/config"
)

func TestLegacyLayoutsEndpointIsNotRegistered(t *testing.T) {
	store := &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	server, err := New(store, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/layouts", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/layouts: want 404, got %d", rec.Code)
	}
}
