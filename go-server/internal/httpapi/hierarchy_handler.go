package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"go-server/internal/hierarchy"

	"github.com/gin-gonic/gin"
)

type HierarchyService interface {
	Store(ctx context.Context, root *hierarchy.Node) error
	Get(ctx context.Context, nodeID int64) (*hierarchy.Node, error)
}

type HierarchyHandler struct {
	service      HierarchyService
	maxBodyBytes int64
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewHierarchyHandler(service HierarchyService, maxBodyBytes int64) *HierarchyHandler {
	return &HierarchyHandler{
		service:      service,
		maxBodyBytes: maxBodyBytes,
	}
}

func (h *HierarchyHandler) RegisterRoutes(router gin.IRouter) {
	router.POST("/hierarchy", h.postHierarchy)
	router.GET("/hierarchy/:node_id", h.getHierarchy)
}

func (h *HierarchyHandler) postHierarchy(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()

	var root hierarchy.Node
	if err := decoder.Decode(&root); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_json", "request body must contain one valid hierarchy object")
		return
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON object")
		return
	}

	if err := h.service.Store(c.Request.Context(), &root); err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeError(c, http.StatusGatewayTimeout, "operation_timeout", "hierarchy operation timed out")
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, hierarchy.ErrUnavailable):
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "hierarchy store is temporarily unavailable")
		case errors.Is(err, hierarchy.ErrHierarchyTooLarge):
			writeError(c, http.StatusRequestEntityTooLarge, "hierarchy_too_large", "hierarchy contains too many nodes")
		case errors.Is(err, hierarchy.ErrInvalidHierarchy), errors.Is(err, hierarchy.ErrHierarchyTooDeep):
			writeError(c, http.StatusUnprocessableEntity, "invalid_hierarchy", err.Error())
		case errors.Is(err, hierarchy.ErrNodeConflict):
			writeError(c, http.StatusConflict, "node_conflict", "a node id belongs to another hierarchy")
		default:
			writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HierarchyHandler) getHierarchy(c *gin.Context) {
	nodeID, err := strconv.ParseInt(c.Param("node_id"), 10, 64)
	if err != nil || nodeID <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_node_id", "node_id must be a positive integer")
		return
	}

	root, err := h.service.Get(c.Request.Context(), nodeID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeError(c, http.StatusGatewayTimeout, "operation_timeout", "hierarchy operation timed out")
		case errors.Is(err, context.Canceled):
			return
		case errors.Is(err, hierarchy.ErrUnavailable):
			writeError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "hierarchy store is temporarily unavailable")
		case errors.Is(err, hierarchy.ErrNotFound):
			writeError(c, http.StatusNotFound, "not_found", "hierarchy node was not found")
		default:
			writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	c.JSON(http.StatusOK, root)
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorEnvelope{
		Error: apiError{
			Code:    code,
			Message: message,
		},
	})
}
