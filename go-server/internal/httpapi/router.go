package httpapi

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func NewRouter(
	service HierarchyService,
	pinger Pinger,
	logger *slog.Logger,
	maxBodyBytes int64,
	operationTimeout time.Duration,
) *gin.Engine {
	router := gin.New()
	router.Use(RequestID(), AccessLog(logger), Recovery(logger), OperationTimeout(operationTimeout))

	NewHealthHandler(pinger).RegisterRoutes(router)
	NewHierarchyHandler(service, maxBodyBytes).RegisterRoutes(router)
	return router
}
