package consumer

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestParseEnvelope(t *testing.T) {
	msg := redis.XMessage{
		ID: "1700000000000-0",
		Values: map[string]any{
			"client_request_id": "client-1",
			"request_id":        "req-1",
			"timestamp":         "2026-04-10T00:00:00Z",
			"user_id":           "42",
			"api_key_id":        "7",
			"group_id":          "3",
			"endpoint":          "/v1/messages",
			"model":             "claude-sonnet-4",
			"stream":            "true",
			"transport":         "http",
			"ws_turn":           "0",
			"sample_reason":     "random",
			"body_bytes":        "123",
			"body_truncated":    "false",
			"body":              `{"messages":[{"role":"user","content":"hello"}]}`,
		},
	}

	envelope, err := ParseEnvelope(msg)
	require.NoError(t, err)
	require.Equal(t, "1700000000000-0", envelope.MessageID)
	require.Equal(t, "client-1", envelope.ClientRequestID)
	require.Equal(t, "req-1", envelope.RequestID)
	require.Equal(t, "/v1/messages", envelope.Endpoint)
	require.Equal(t, "claude-sonnet-4", envelope.Model)
	require.Equal(t, "http", envelope.Transport)
	require.Equal(t, "random", envelope.SampleReason)
	require.True(t, envelope.Stream)
	require.Equal(t, 123, envelope.BodyBytes)
	require.False(t, envelope.BodyTruncated)
	require.Equal(t, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC), envelope.Timestamp)
	require.NotNil(t, envelope.UserID)
	require.Equal(t, int64(42), *envelope.UserID)
	require.NotNil(t, envelope.APIKeyID)
	require.Equal(t, int64(7), *envelope.APIKeyID)
	require.NotNil(t, envelope.GroupID)
	require.Equal(t, int64(3), *envelope.GroupID)
}
