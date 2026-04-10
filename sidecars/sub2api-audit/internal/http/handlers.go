package audithttp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/config"
	"github.com/Wei-Shaw/sub2api-audit/internal/consumer"
	"github.com/Wei-Shaw/sub2api-audit/internal/model"
	"github.com/Wei-Shaw/sub2api-audit/internal/repository"
	"github.com/gin-gonic/gin"
)

type Handlers struct {
	cfg          *config.Config
	settingsRepo *repository.SettingsRepository
	auditRepo    *repository.PromptAuditLogRepository
	statusStore  *consumer.StatusStore
}

type putConfigRequest struct {
	ConsumerPaused bool     `json:"consumer_paused"`
	KeywordRules   []string `json:"keyword_rules"`
}

func NewHandlers(
	cfg *config.Config,
	settingsRepo *repository.SettingsRepository,
	auditRepo *repository.PromptAuditLogRepository,
	statusStore *consumer.StatusStore,
) *Handlers {
	return &Handlers{
		cfg:          cfg,
		settingsRepo: settingsRepo,
		auditRepo:    auditRepo,
		statusStore:  statusStore,
	}
}

func (h *Handlers) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

func (h *Handlers) ListLogs(c *gin.Context) {
	filter, err := h.parseLogFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_REQUEST", "message": err.Error()}})
		return
	}

	items, total, err := h.auditRepo.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "failed to list logs"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items":  items,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (h *Handlers) Stats(c *gin.Context) {
	filter, err := h.parseLogFilter(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_REQUEST", "message": err.Error()}})
		return
	}
	filter.Limit = 0
	filter.Offset = 0

	stats, err := h.auditRepo.Stats(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "failed to compute stats"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stats":    stats,
		"consumer": h.statusStore.Snapshot(),
	})
}

func (h *Handlers) GetConfig(c *gin.Context) {
	runtimeConfig, err := h.settingsRepo.GetRuntimeConfig(c.Request.Context(), h.cfg.Audit.KeywordRules)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "failed to load runtime config"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"runtime": runtimeConfig,
		"static": gin.H{
			"stream_name":      h.cfg.Audit.StreamName,
			"consumer_group":   h.cfg.Audit.ConsumerGroup,
			"consumer_name":    h.cfg.Audit.ConsumerName,
			"consumer_enabled": h.cfg.Audit.ConsumerEnabled,
			"default_limit":    h.cfg.Audit.DefaultListLimit,
			"max_limit":        h.cfg.Audit.MaxListLimit,
		},
	})
}

func (h *Handlers) PutConfig(c *gin.Context) {
	var req putConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "BAD_REQUEST", "message": "invalid request body"}})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	stored, err := h.settingsRepo.SetRuntimeConfig(ctx, model.RuntimeConfig{
		ConsumerPaused: req.ConsumerPaused,
		KeywordRules:   req.KeywordRules,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "failed to store runtime config"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"runtime": stored,
	})
}

func (h *Handlers) parseLogFilter(c *gin.Context) (model.ListLogsFilter, error) {
	filter := model.ListLogsFilter{
		Model:        strings.TrimSpace(c.Query("model")),
		Transport:    strings.TrimSpace(c.Query("transport")),
		SampleReason: strings.TrimSpace(c.Query("sample_reason")),
		RiskTag:      strings.TrimSpace(c.Query("risk_tag")),
		Limit:        h.cfg.Audit.DefaultListLimit,
		Offset:       0,
	}

	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit <= 0 {
			return model.ListLogsFilter{}, errInvalidQuery("limit")
		}
		if limit > h.cfg.Audit.MaxListLimit {
			limit = h.cfg.Audit.MaxListLimit
		}
		filter.Limit = limit
	}
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		offset, err := strconv.Atoi(v)
		if err != nil || offset < 0 {
			return model.ListLogsFilter{}, errInvalidQuery("offset")
		}
		filter.Offset = offset
	}

	if value, ok, err := parseOptionalInt64(c.Query("user_id")); err != nil {
		return model.ListLogsFilter{}, err
	} else if ok {
		filter.UserID = &value
	}
	if value, ok, err := parseOptionalInt64(c.Query("api_key_id")); err != nil {
		return model.ListLogsFilter{}, err
	} else if ok {
		filter.APIKeyID = &value
	}
	if value, ok, err := parseOptionalInt64(c.Query("group_id")); err != nil {
		return model.ListLogsFilter{}, err
	} else if ok {
		filter.GroupID = &value
	}
	if parsed, ok, err := parseOptionalTime(c.Query("from")); err != nil {
		return model.ListLogsFilter{}, err
	} else if ok {
		filter.From = &parsed
	}
	if parsed, ok, err := parseOptionalTime(c.Query("to")); err != nil {
		return model.ListLogsFilter{}, err
	} else if ok {
		filter.To = &parsed
	}
	return filter, nil
}

func parseOptionalInt64(raw string) (int64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, errInvalidQuery(raw)
	}
	return value, true, nil
}

func parseOptionalTime(raw string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, errInvalidQuery(raw)
}

func errInvalidQuery(name string) error {
	return &queryError{name: name}
}

type queryError struct {
	name string
}

func (e *queryError) Error() string {
	return "invalid query parameter: " + e.name
}
