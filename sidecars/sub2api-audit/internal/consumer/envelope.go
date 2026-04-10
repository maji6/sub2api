package consumer

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
	"github.com/redis/go-redis/v9"
)

func ParseEnvelope(message redis.XMessage) (model.AuditEnvelope, error) {
	raw := make(map[string]any, len(message.Values)+1)
	raw["redis_message_id"] = message.ID
	for key, value := range message.Values {
		raw[key] = value
	}

	envelope := model.AuditEnvelope{
		MessageID:       message.ID,
		ClientRequestID: strings.TrimSpace(asString(message.Values["client_request_id"])),
		RequestID:       strings.TrimSpace(asString(message.Values["request_id"])),
		Endpoint:        strings.TrimSpace(asString(message.Values["endpoint"])),
		Model:           strings.TrimSpace(asString(message.Values["model"])),
		Transport:       strings.TrimSpace(asString(message.Values["transport"])),
		SampleReason:    strings.TrimSpace(asString(message.Values["sample_reason"])),
		Body:            asString(message.Values["body"]),
		Stream:          asBool(message.Values["stream"]),
		WSTurn:          asInt(message.Values["ws_turn"]),
		BodyBytes:       asInt(message.Values["body_bytes"]),
		BodyTruncated:   asBool(message.Values["body_truncated"]),
		RawEnvelope:     raw,
	}

	if parsedTime, ok := parseTime(message.Values["timestamp"]); ok {
		envelope.Timestamp = parsedTime
	} else {
		envelope.Timestamp = time.Now().UTC()
	}

	if value, ok := asOptionalInt64(message.Values["user_id"]); ok {
		envelope.UserID = &value
	}
	if value, ok := asOptionalInt64(message.Values["api_key_id"]); ok {
		envelope.APIKeyID = &value
	}
	if value, ok := asOptionalInt64(message.Values["group_id"]); ok {
		envelope.GroupID = &value
	}

	switch {
	case envelope.Endpoint == "":
		return model.AuditEnvelope{}, fmt.Errorf("missing endpoint")
	case envelope.Model == "":
		return model.AuditEnvelope{}, fmt.Errorf("missing model")
	case envelope.Transport == "":
		return model.AuditEnvelope{}, fmt.Errorf("missing transport")
	case envelope.SampleReason == "":
		return model.AuditEnvelope{}, fmt.Errorf("missing sample_reason")
	case strings.TrimSpace(envelope.Body) == "":
		return model.AuditEnvelope{}, fmt.Errorf("missing body")
	}

	return envelope, nil
}

func asString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}

func asBool(v any) bool {
	switch strings.ToLower(strings.TrimSpace(asString(v))) {
	case "1", "true", "t", "yes", "y":
		return true
	default:
		return false
	}
}

func asInt(v any) int {
	if parsed, err := strconv.Atoi(strings.TrimSpace(asString(v))); err == nil {
		return parsed
	}
	return 0
}

func asOptionalInt64(v any) (int64, bool) {
	raw := strings.TrimSpace(asString(v))
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseTime(v any) (time.Time, bool) {
	raw := strings.TrimSpace(asString(v))
	if raw == "" {
		return time.Time{}, false
	}

	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
