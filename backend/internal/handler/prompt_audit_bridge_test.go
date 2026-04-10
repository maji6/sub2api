package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type promptAuditCapturedEvent struct {
	stream string
	values map[string]any
}

func TestPromptAuditBridgeMiddleware_SampledHTTPPublishesSanitizedPayload(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(10000, 128*1024), promptAuditAuthMiddleware())

	rawBody := []byte(`{"model":"claude-sonnet-4","api_key":"sk-secret-123","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, rawBody)
		c.Status(http.StatusNoContent)
	})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, *events, 1)

	event := (*events)[0]
	require.Equal(t, "prompt_audit:sampled", event.stream)
	require.Equal(t, "/v1/messages", event.values["endpoint"])
	require.Equal(t, "claude-sonnet-4", event.values["model"])
	require.Equal(t, "http", event.values["transport"])
	require.Equal(t, 0, event.values["ws_turn"])
	require.Equal(t, "random", event.values["sample_reason"])
	require.Equal(t, false, event.values["stream"])
	require.Equal(t, int64(42), event.values["user_id"])
	require.Equal(t, int64(9), event.values["api_key_id"])
	require.Equal(t, int64(7), event.values["group_id"])
	require.Equal(t, len(rawBody), event.values["body_bytes"])
	require.Equal(t, false, event.values["body_truncated"])
	require.NotEmpty(t, event.values["request_id"])
	require.NotEmpty(t, event.values["client_request_id"])

	body, ok := event.values["body"].(string)
	require.True(t, ok)
	require.Contains(t, body, "[REDACTED]")
	require.NotContains(t, body, "sk-secret-123")
}

func TestPromptAuditBridgeMiddleware_SkipWhenSampleMissesAndNoForcedReason(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(0, 128*1024), promptAuditAuthMiddleware())

	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, *events)
}

func TestPromptAuditBridgeMiddleware_ForcedToolsPublishesAtZeroSampleRate(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(0, 128*1024), promptAuditAuthMiddleware())

	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, []byte(`{"model":"claude-sonnet-4","tools":[{"name":"web_search"}],"messages":[{"role":"user","content":"hello"}]}`))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, *events, 1)
	require.Equal(t, "forced_tools", (*events)[0].values["sample_reason"])
}

func TestPromptAuditBridgeMiddleware_ForcedBase64SummarizesBody(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(0, 128*1024), promptAuditAuthMiddleware())

	rawBody := []byte(`{"model":"gpt-4.1","input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,QUJDREVGRw=="}]}]}`)
	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "gpt-4.1", false, rawBody)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, *events, 1)
	require.Equal(t, "forced_base64", (*events)[0].values["sample_reason"])

	body, ok := (*events)[0].values["body"].(string)
	require.True(t, ok)
	require.Contains(t, body, "[base64: ~7bytes]")
	require.NotContains(t, body, "QUJDREVGRw==")
}

func TestPromptAuditBridgeMiddleware_ForcedLongPromptPublishesTruncatedBody(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(0, 64), promptAuditAuthMiddleware())

	rawBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + strings.Repeat("x", 512) + `"}]}`)
	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, rawBody)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, *events, 1)
	require.Equal(t, "forced_long_prompt", (*events)[0].values["sample_reason"])
	require.Equal(t, true, (*events)[0].values["body_truncated"])

	body, ok := (*events)[0].values["body"].(string)
	require.True(t, ok)
	require.Contains(t, body, "request_body_truncated")
}

func TestPromptAuditBridgeMiddleware_MarksWSFirstTurn(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(10000, 128*1024), promptAuditAuthMiddleware())

	firstMessage := []byte(`{"model":"gpt-4.1","input":"hello"}`)
	router.GET("/v1/responses", func(c *gin.Context) {
		setOpsRequestContext(c, "gpt-4.1", true, firstMessage)
		c.Status(http.StatusSwitchingProtocols)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusSwitchingProtocols, w.Code)
	require.Len(t, *events, 1)

	event := (*events)[0]
	require.Equal(t, "ws", event.values["transport"])
	require.Equal(t, 1, event.values["ws_turn"])
	require.Equal(t, true, event.values["stream"])
	require.Equal(t, "/v1/responses", event.values["endpoint"])
}

func TestPromptAuditBridgeMiddleware_SkipWithoutAPIKeyContext(t *testing.T) {
	events := capturePromptAuditEvents(t, nil)
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(10000, 128*1024), func(c *gin.Context) {
		c.Next()
	})

	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, *events)
}

func TestPromptAuditBridgeMiddleware_FailOpenWhenPublishFails(t *testing.T) {
	events := capturePromptAuditEvents(t, errors.New("redis unavailable"))
	router := newPromptAuditBridgeTestRouter(t, newPromptAuditTestConfig(10000, 128*1024), promptAuditAuthMiddleware())

	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "claude-sonnet-4", false, []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hello"}]}`))
		c.Status(http.StatusAccepted)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, *events, 1)
}

func newPromptAuditBridgeTestRouter(t *testing.T, cfg *config.Config, auth gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(servermiddleware.RequestLogger())
	router.Use(servermiddleware.ClientRequestID())
	router.Use(OpsErrorLoggerMiddleware(nil))
	router.Use(PromptAuditBridgeMiddleware(newPromptAuditTestRedisClient(t), cfg))
	router.Use(InboundEndpointMiddleware())
	if auth != nil {
		router.Use(auth)
	}
	return router
}

func newPromptAuditTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func newPromptAuditTestConfig(sampleRateBasisPoints int, maxBodyBytes int) *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			PromptAudit: config.GatewayPromptAuditConfig{
				Enabled:               true,
				StreamName:            "prompt_audit:sampled",
				SampleRateBasisPoints: sampleRateBasisPoints,
				MaxBodyBytes:          maxBodyBytes,
			},
		},
	}
}

func promptAuditAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID := int64(7)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			ID:      9,
			GroupID: &groupID,
			User:    &service.User{ID: 42},
		})
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{
			UserID:      42,
			Concurrency: 1,
		})
		c.Next()
	}
}

func capturePromptAuditEvents(t *testing.T, publishErr error) *[]promptAuditCapturedEvent {
	t.Helper()

	events := make([]promptAuditCapturedEvent, 0, 1)
	original := promptAuditXAdd
	promptAuditXAdd = func(ctx context.Context, client *redis.Client, args *redis.XAddArgs) error {
		t.Helper()
		require.NotNil(t, ctx)
		require.NotNil(t, client)
		require.NotNil(t, args)

		values, ok := args.Values.(map[string]any)
		require.True(t, ok)

		copied := make(map[string]any, len(values))
		for k, v := range values {
			copied[k] = v
		}
		events = append(events, promptAuditCapturedEvent{
			stream: args.Stream,
			values: copied,
		})
		return publishErr
	}
	t.Cleanup(func() {
		promptAuditXAdd = original
	})
	return &events
}
