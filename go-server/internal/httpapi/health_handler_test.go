package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakePinger struct {
	err error
}

func (f fakePinger) PingContext(context.Context) error {
	return f.err
}

func TestHealthRoutesSeparateLivenessAndReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("liveness does not require database", func(t *testing.T) {
		router := gin.New()
		NewHealthHandler(fakePinger{err: errors.New("database down")}).RegisterRoutes(router)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
	})

	t.Run("readiness fails when database is down", func(t *testing.T) {
		router := gin.New()
		NewHealthHandler(fakePinger{err: errors.New("database down")}).RegisterRoutes(router)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))

		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", response.Code)
		}
	})
}
