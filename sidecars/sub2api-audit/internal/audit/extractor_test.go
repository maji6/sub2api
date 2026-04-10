package audit

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
	"github.com/stretchr/testify/require"
)

func TestBuildPromptAuditLog_ExtractsSummariesAndRiskTags(t *testing.T) {
	envelope := model.AuditEnvelope{
		MessageID:       "1700000000000-0",
		ClientRequestID: "client-1",
		Timestamp:       time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		Endpoint:        "/v1/messages",
		Model:           "claude-sonnet-4",
		Transport:       "http",
		SampleReason:    "forced_base64",
		Body: `{
			"instructions":"system guidance",
			"tools":[{"name":"web_search"}],
			"messages":[
				{"role":"user","content":"please help with password reset"},
				{"role":"assistant","content":"sure"}
			],
			"attachments":["[base64: ~7bytes]"],
			"api_key":"[REDACTED]"
		}`,
		RawEnvelope: map[string]any{"sample_reason": "forced_base64"},
	}

	item := BuildPromptAuditLog(envelope, model.RuntimeConfig{
		KeywordRules: []string{"password reset"},
	})

	require.Equal(t, "system guidance", item.SystemText)
	require.Contains(t, item.ConversationExcerpt, "user: please help with password reset")
	require.Equal(t, []string{"web_search"}, item.ToolSummary)
	require.Equal(t, 1, item.AttachmentSummary.Base64Count)
	require.Equal(t, 7, item.AttachmentSummary.ApproxBytes)
	require.Contains(t, item.RiskTags, model.DefaultCredentialRiskTag)
	require.Contains(t, item.RiskTags, model.DefaultToolRiskTag)
	require.Contains(t, item.RiskTags, model.DefaultAttachmentRiskTag)
	require.Contains(t, item.RiskTags, "keyword:password reset")
}

func TestBuildPromptAuditLog_LongPromptAddsRiskTag(t *testing.T) {
	item := BuildPromptAuditLog(model.AuditEnvelope{
		MessageID:       "1700000000001-0",
		ClientRequestID: "client-2",
		Timestamp:       time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		Endpoint:        "/v1/responses",
		Model:           "gpt-4.1",
		Transport:       "ws",
		SampleReason:    "forced_long_prompt",
		BodyTruncated:   true,
		Body:            `{"messages":[{"role":"user","content":"hello"}]}`,
	}, model.RuntimeConfig{})

	require.Contains(t, item.RiskTags, model.DefaultLongPromptRiskTag)
}
