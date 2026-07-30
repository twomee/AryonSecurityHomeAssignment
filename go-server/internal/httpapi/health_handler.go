package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Pinger interface {
	PingContext(ctx context.Context) error
}

type HealthHandler struct {
	pinger Pinger
}

func NewHealthHandler(pinger Pinger) *HealthHandler {
	return &HealthHandler{pinger: pinger}
}

func (h *HealthHandler) RegisterRoutes(router gin.IRouter) {
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", h.ready)
}

func (h *HealthHandler) ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	if err := h.pinger.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"checks": gin.H{"database": "unavailable"},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"checks": gin.H{"database": "ok"},
	})
}
