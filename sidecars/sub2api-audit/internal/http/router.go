package audithttp

import (
	"github.com/Wei-Shaw/sub2api-audit/internal/config"
	"github.com/Wei-Shaw/sub2api-audit/internal/consumer"
	"github.com/Wei-Shaw/sub2api-audit/internal/repository"
	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	Config       *config.Config
	SettingsRepo *repository.SettingsRepository
	AuditRepo    *repository.PromptAuditLogRepository
	StatusStore  *consumer.StatusStore
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	handlers := NewHandlers(deps.Config, deps.SettingsRepo, deps.AuditRepo, deps.StatusStore)
	adminAuth := AdminAPIKeyAuth(deps.SettingsRepo, deps.Config.AdminAuth.AdminAPIKey)

	router.GET("/healthz", handlers.Healthz)

	auditGroup := router.Group("/api/v1/audit")
	auditGroup.Use(adminAuth)
	{
		auditGroup.GET("/logs", handlers.ListLogs)
		auditGroup.GET("/stats", handlers.Stats)
		auditGroup.GET("/config", handlers.GetConfig)
		auditGroup.PUT("/config", handlers.PutConfig)
	}

	return router
}
