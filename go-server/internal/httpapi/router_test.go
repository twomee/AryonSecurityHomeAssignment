package httpapi

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-server/internal/hierarchy"
)

func TestNewRouterRegistersHierarchyAndHealthRoutes(t *testing.T) {
	service := &fakeHierarchyService{
		result: &hierarchy.Node{ID: 1, Type: "management_group", Children: []*hierarchy.Node{}},
	}
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	router := NewRouter(service, fakePinger{}, logger, 1024, time.Second)

	for _, path := range []string{"/healthz", "/readyz", "/hierarchy/1"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200; body=%s", path, response.Code, response.Body.String())
		}
	}
}
