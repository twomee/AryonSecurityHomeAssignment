package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go-server/internal/hierarchy"

	"github.com/gin-gonic/gin"
)

type fakeHierarchyService struct {
	stored   *hierarchy.Node
	storeErr error
	result   *hierarchy.Node
	getErr   error
	gotID    int64
}

func (f *fakeHierarchyService) Store(_ context.Context, node *hierarchy.Node) error {
	f.stored = node
	return f.storeErr
}

func (f *fakeHierarchyService) Get(_ context.Context, nodeID int64) (*hierarchy.Node, error) {
	f.gotID = nodeID
	return f.result, f.getErr
}

func newTestRouter(service HierarchyService, maxBodyBytes int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewHierarchyHandler(service, maxBodyBytes)
	handler.RegisterRoutes(router)
	return router
}

func TestPostHierarchyStoresValidTree(t *testing.T) {
	service := &fakeHierarchyService{}
	router := newTestRouter(service, 1024)
	body := []byte(`{"id":1,"type":"management_group","children":[]}`)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hierarchy", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	want := &hierarchy.Node{ID: 1, Type: "management_group", Children: []*hierarchy.Node{}}
	if !reflect.DeepEqual(service.stored, want) {
		t.Fatalf("stored node = %#v, want %#v", service.stored, want)
	}
}

func TestPostHierarchyRejectsMalformedOrUnknownJSON(t *testing.T) {
	testCases := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"id":1`},
		{name: "unknown field", body: `{"id":1,"type":"management_group","children":[],"surprise":true}`},
		{name: "multiple values", body: `{"id":1,"type":"management_group","children":[]} {}`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			router := newTestRouter(&fakeHierarchyService{}, 1024)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hierarchy", bytes.NewBufferString(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPostHierarchyMapsDomainErrors(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid", err: hierarchy.ErrInvalidHierarchy, wantStatus: http.StatusUnprocessableEntity},
		{name: "too deep", err: hierarchy.ErrHierarchyTooDeep, wantStatus: http.StatusUnprocessableEntity},
		{name: "too large", err: hierarchy.ErrHierarchyTooLarge, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "conflict", err: hierarchy.ErrNodeConflict, wantStatus: http.StatusConflict},
		{name: "unavailable", err: hierarchy.ErrUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, wantStatus: http.StatusGatewayTimeout},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &fakeHierarchyService{storeErr: testCase.err}
			router := newTestRouter(service, 1024)
			request := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"/hierarchy",
				bytes.NewBufferString(`{"id":1,"type":"management_group","children":[]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, testCase.wantStatus, response.Body.String())
			}
		})
	}
}

func TestPostHierarchyRejectsOversizedBody(t *testing.T) {
	router := newTestRouter(&fakeHierarchyService{}, 16)
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/hierarchy",
		bytes.NewBufferString(`{"id":1,"type":"management_group","children":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", response.Code, response.Body.String())
	}
}

func TestGetHierarchyReturnsOrderedJSONWithEmptyChildrenArray(t *testing.T) {
	service := &fakeHierarchyService{
		result: &hierarchy.Node{
			ID:   3,
			Type: "subscription",
			Children: []*hierarchy.Node{
				{ID: 4, Type: "resource_group", Children: []*hierarchy.Node{}},
			},
		},
	}
	router := newTestRouter(service, 1024)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/hierarchy/3", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if service.gotID != 3 {
		t.Fatalf("service node id = %d, want 3", service.gotID)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	children := body["children"].([]any)
	leaf := children[0].(map[string]any)
	if leafChildren, ok := leaf["children"].([]any); !ok || len(leafChildren) != 0 {
		t.Fatalf("leaf children = %#v, want []", leaf["children"])
	}
}

func TestGetHierarchyMapsInvalidAndMissingIDs(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		router := newTestRouter(&fakeHierarchyService{}, 1024)
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/hierarchy/not-a-number", nil)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("missing node", func(t *testing.T) {
		router := newTestRouter(&fakeHierarchyService{getErr: hierarchy.ErrNotFound}, 1024)
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/hierarchy/999", nil)
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
	})
}
