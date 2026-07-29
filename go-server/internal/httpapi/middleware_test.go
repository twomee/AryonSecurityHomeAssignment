package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestOperationTimeoutCancelsHandlerContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(OperationTimeout(time.Millisecond))
	router.GET("/", func(c *gin.Context) {
		<-c.Request.Context().Done()
		if !errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
			t.Fatalf("request context error = %v, want deadline exceeded", c.Request.Context().Err())
		}
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil),
	)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}

func TestRequestIDMiddlewarePreservesOrGeneratesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, testCase := range []struct {
		name     string
		provided string
	}{
		{name: "preserves provided id", provided: "request-123"},
		{name: "generates missing id"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(RequestID())
			router.GET("/", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if testCase.provided != "" {
				request.Header.Set("X-Request-ID", testCase.provided)
			}
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			got := response.Header().Get("X-Request-ID")
			if got == "" {
				t.Fatal("response request id is empty")
			}
			if testCase.provided != "" && got != testCase.provided {
				t.Fatalf("response request id = %q, want %q", got, testCase.provided)
			}
		})
	}
}

func TestAccessLogWritesStructuredRequestFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	router := gin.New()
	router.Use(RequestID(), AccessLog(logger))
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil),
	)

	logged := output.String()
	for _, field := range []string{`"method":"GET"`, `"path":"/"`, `"status":204`, `"request_id":`} {
		if !strings.Contains(logged, field) {
			t.Fatalf("structured log %q does not contain %q", logged, field)
		}
	}
}

func TestRecoveryReturnsSafeJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	router := gin.New()
	router.Use(RequestID(), Recovery(logger))
	router.GET("/", func(*gin.Context) {
		panic("secret internal detail")
	})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret internal detail") {
		t.Fatalf("response leaked panic: %s", response.Body.String())
	}
}
