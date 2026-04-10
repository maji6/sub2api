package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	promptAuditPublishTimeout         = 2 * time.Second
	promptAuditTransportHTTP          = "http"
	promptAuditTransportWS            = "ws"
	promptAuditSampleReasonRandom     = "random"
	promptAuditSampleReasonForcedTool = "forced_tools"
	promptAuditSampleReasonForcedB64  = "forced_base64"
	promptAuditSampleReasonForcedLong = "forced_long_prompt"
)

var promptAuditXAdd = func(ctx context.Context, client *redis.Client, args *redis.XAddArgs) error {
	return client.XAdd(ctx, args).Err()
}

type promptAuditPayloadInspection struct {
	hasTools  bool
	hasBase64 bool
}

// PromptAuditBridgeMiddleware samples sanitized prompt payloads into a Redis Stream.
func PromptAuditBridgeMiddleware(redisClient *redis.Client, cfg *config.Config) gin.HandlerFunc {
	enabled, streamName, sampleRateBasisPoints, maxBodyBytes := promptAuditBridgeConfig(cfg)
	if !enabled || redisClient == nil || streamName == "" {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		c.Next()

		values, ok := buildPromptAuditStreamValues(c, sampleRateBasisPoints, maxBodyBytes)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), promptAuditPublishTimeout)
		defer cancel()

		if err := promptAuditXAdd(ctx, redisClient, &redis.XAddArgs{
			Stream: streamName,
			Values: values,
		}); err != nil {
			log.Printf("[PromptAuditBridge] publish failed: %v", err)
		}
	}
}

func promptAuditBridgeConfig(cfg *config.Config) (enabled bool, streamName string, sampleRateBasisPoints int, maxBodyBytes int) {
	if cfg == nil {
		return false, "", 0, 0
	}
	promptAuditCfg := cfg.Gateway.PromptAudit
	return promptAuditCfg.Enabled,
		strings.TrimSpace(promptAuditCfg.StreamName),
		promptAuditCfg.SampleRateBasisPoints,
		promptAuditCfg.MaxBodyBytes
}

func buildPromptAuditStreamValues(c *gin.Context, sampleRateBasisPoints int, maxBodyBytes int) (map[string]any, bool) {
	if c == nil || c.Request == nil {
		return nil, false
	}

	model, ok := promptAuditModelFromContext(c)
	if !ok {
		return nil, false
	}
	rawBody, ok := promptAuditRequestBodyFromContext(c)
	if !ok {
		return nil, false
	}

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return nil, false
	}

	clientRequestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	sampleReason := promptAuditSampleReason(clientRequestID, rawBody, sampleRateBasisPoints, maxBodyBytes)
	if sampleReason == "" {
		return nil, false
	}

	body, bodyTruncated, bodyBytes, ok := promptAuditPrepareBody(rawBody, maxBodyBytes)
	if !ok {
		return nil, false
	}

	transport := promptAuditTransport(c)
	wsTurn := 0
	if transport == promptAuditTransportWS {
		wsTurn = 1
	}

	values := map[string]any{
		"client_request_id": strings.TrimSpace(clientRequestID),
		"request_id":        strings.TrimSpace(requestID),
		"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
		"endpoint":          GetInboundEndpoint(c),
		"model":             model,
		"stream":            promptAuditStreamFromContext(c),
		"transport":         transport,
		"ws_turn":           wsTurn,
		"sample_reason":     sampleReason,
		"body_bytes":        bodyBytes,
		"body_truncated":    bodyTruncated,
		"body":              body,
	}

	if apiKey.ID > 0 {
		values["api_key_id"] = apiKey.ID
	}
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 {
		values["group_id"] = *apiKey.GroupID
	}
	if userID := promptAuditUserID(c, apiKey); userID > 0 {
		values["user_id"] = userID
	}

	return values, true
}

func promptAuditModelFromContext(c *gin.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	v, ok := c.Get(opsModelKey)
	if !ok {
		return "", false
	}
	model, ok := v.(string)
	model = strings.TrimSpace(model)
	return model, ok && model != ""
}

func promptAuditRequestBodyFromContext(c *gin.Context) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.Get(opsRequestBodyKey)
	if !ok {
		return nil, false
	}
	raw, ok := v.([]byte)
	return raw, ok && len(raw) > 0
}

func promptAuditStreamFromContext(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(opsStreamKey)
	if !ok {
		return false
	}
	stream, _ := v.(bool)
	return stream
}

func promptAuditUserID(c *gin.Context, apiKey *service.APIKey) int64 {
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		return subject.UserID
	}
	if apiKey != nil && apiKey.User != nil && apiKey.User.ID > 0 {
		return apiKey.User.ID
	}
	return 0
}

func promptAuditTransport(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return promptAuditTransportHTTP
	}
	upgrade := strings.TrimSpace(strings.ToLower(c.GetHeader("Upgrade")))
	connection := strings.TrimSpace(strings.ToLower(c.GetHeader("Connection")))
	if upgrade == "websocket" && strings.Contains(connection, "upgrade") {
		return promptAuditTransportWS
	}
	return promptAuditTransportHTTP
}

func promptAuditSampleReason(clientRequestID string, rawBody []byte, sampleRateBasisPoints int, maxBodyBytes int) string {
	inspection := inspectPromptAuditPayload(rawBody)
	switch {
	case inspection.hasTools:
		return promptAuditSampleReasonForcedTool
	case inspection.hasBase64:
		return promptAuditSampleReasonForcedB64
	case maxBodyBytes > 0 && len(rawBody) > maxBodyBytes:
		return promptAuditSampleReasonForcedLong
	}

	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" || sampleRateBasisPoints <= 0 {
		return ""
	}
	if sampleRateBasisPoints >= 10000 {
		return promptAuditSampleReasonRandom
	}
	if int(xxhash.Sum64String(clientRequestID)%10000) < sampleRateBasisPoints {
		return promptAuditSampleReasonRandom
	}
	return ""
}

func inspectPromptAuditPayload(rawBody []byte) promptAuditPayloadInspection {
	if len(rawBody) == 0 {
		return promptAuditPayloadInspection{}
	}

	var decoded any
	if err := json.Unmarshal(rawBody, &decoded); err != nil {
		return promptAuditPayloadInspection{
			hasBase64: promptAuditHasDataURIBase64(string(rawBody)),
		}
	}

	inspection := promptAuditPayloadInspection{}
	promptAuditInspectValue(decoded, &inspection)
	return inspection
}

func promptAuditInspectValue(v any, inspection *promptAuditPayloadInspection) {
	if inspection == nil || (inspection.hasTools && inspection.hasBase64) {
		return
	}

	switch value := v.(type) {
	case map[string]any:
		if promptAuditHasNonEmptyTools(value) {
			inspection.hasTools = true
		}
		if promptAuditMapDeclaresBase64(value) {
			inspection.hasBase64 = true
		}
		for _, child := range value {
			promptAuditInspectValue(child, inspection)
			if inspection.hasTools && inspection.hasBase64 {
				return
			}
		}
	case []any:
		for _, child := range value {
			promptAuditInspectValue(child, inspection)
			if inspection.hasTools && inspection.hasBase64 {
				return
			}
		}
	case string:
		if promptAuditHasDataURIBase64(value) {
			inspection.hasBase64 = true
		}
	}
}

func promptAuditHasNonEmptyTools(v map[string]any) bool {
	rawTools, ok := v["tools"]
	if !ok || rawTools == nil {
		return false
	}

	switch tools := rawTools.(type) {
	case []any:
		return len(tools) > 0
	case map[string]any:
		return len(tools) > 0
	case string:
		return strings.TrimSpace(tools) != ""
	default:
		return true
	}
}

func promptAuditMapDeclaresBase64(v map[string]any) bool {
	rawType, ok := v["type"]
	if !ok {
		return false
	}
	typ, ok := rawType.(string)
	return ok && strings.EqualFold(strings.TrimSpace(typ), "base64")
}

func promptAuditPrepareBody(rawBody []byte, maxBodyBytes int) (body string, truncated bool, bodyBytes int, ok bool) {
	requestBodyJSON, truncated, requestBodyBytes := service.PrepareOpsRequestBodyForQueueWithLimit(rawBody, maxBodyBytes)
	if requestBodyJSON == nil || requestBodyBytes == nil {
		return "", truncated, 0, false
	}

	body = *requestBodyJSON
	if summarized, changed := promptAuditSummarizeBase64JSON(body); changed {
		body = summarized
	}
	return body, truncated, *requestBodyBytes, strings.TrimSpace(body) != ""
}

func promptAuditSummarizeBase64JSON(rawJSON string) (string, bool) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return "", false
	}

	var decoded any
	if err := json.Unmarshal([]byte(rawJSON), &decoded); err != nil {
		return rawJSON, false
	}

	summarized, changed := promptAuditSummarizeValue(decoded)
	if !changed {
		return rawJSON, false
	}

	encoded, err := json.Marshal(summarized)
	if err != nil {
		return rawJSON, false
	}
	return string(encoded), true
}

func promptAuditSummarizeValue(v any) (any, bool) {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		changed := false
		isBase64Object := promptAuditMapDeclaresBase64(value)
		for key, child := range value {
			if isBase64Object && strings.EqualFold(strings.TrimSpace(key), "data") {
				if summarized, ok := promptAuditSummarizeKnownBase64Value(child); ok {
					out[key] = summarized
					changed = true
					continue
				}
			}
			next, nextChanged := promptAuditSummarizeValue(child)
			out[key] = next
			changed = changed || nextChanged
		}
		return out, changed
	case []any:
		out := make([]any, len(value))
		changed := false
		for i, child := range value {
			next, nextChanged := promptAuditSummarizeValue(child)
			out[i] = next
			changed = changed || nextChanged
		}
		return out, changed
	case string:
		if summarized, ok := promptAuditSummarizeDataURIStringValue(value); ok {
			return summarized, true
		}
		return value, false
	default:
		return value, false
	}
}

func promptAuditSummarizeDataURIStringValue(v any) (string, bool) {
	raw, ok := v.(string)
	if !ok {
		return "", false
	}
	return promptAuditBase64Summary(raw)
}

func promptAuditSummarizeKnownBase64Value(v any) (string, bool) {
	raw, ok := v.(string)
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	approxBytes := promptAuditApproxDecodedBase64Bytes(raw)
	if approxBytes <= 0 {
		approxBytes = len(raw)
	}
	return fmt.Sprintf("[base64: ~%dbytes]", approxBytes), true
}

func promptAuditBase64Summary(raw string) (string, bool) {
	if !promptAuditHasDataURIBase64(raw) {
		return "", false
	}

	base64Data := promptAuditExtractBase64Data(raw)
	approxBytes := promptAuditApproxDecodedBase64Bytes(base64Data)
	if approxBytes <= 0 {
		approxBytes = len(strings.TrimSpace(base64Data))
	}
	return fmt.Sprintf("[base64: ~%dbytes]", approxBytes), true
}

func promptAuditHasDataURIBase64(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return strings.Contains(strings.ToLower(raw), ";base64,")
}

func promptAuditExtractBase64Data(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if idx := strings.Index(lower, ";base64,"); idx >= 0 {
		return raw[idx+len(";base64,"):]
	}
	return raw
}

func promptAuditCompactBase64String(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func promptAuditApproxDecodedBase64Bytes(raw string) int {
	compacted := promptAuditCompactBase64String(raw)
	if compacted == "" {
		return 0
	}

	padding := 0
	for i := len(compacted) - 1; i >= 0 && compacted[i] == '='; i-- {
		padding++
	}

	decodedLen := (len(compacted) * 3 / 4) - padding
	if decodedLen < 0 {
		return 0
	}
	return decodedLen
}
