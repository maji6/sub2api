package model

import "time"

const (
	SettingKeyAdminAPIKey         = "admin_api_key"
	SettingKeySidecarPromptConfig = "sidecar_prompt_audit_config"
	DefaultAttachmentRiskTag      = "attachment_base64"
	DefaultCredentialRiskTag      = "credential"
	DefaultToolRiskTag            = "tool_risk"
	DefaultLongPromptRiskTag      = "long_prompt"
)

type AuditEnvelope struct {
	MessageID       string
	ClientRequestID string
	RequestID       string
	Timestamp       time.Time
	UserID          *int64
	APIKeyID        *int64
	GroupID         *int64
	Endpoint        string
	Model           string
	Stream          bool
	Transport       string
	WSTurn          int
	SampleReason    string
	BodyBytes       int
	BodyTruncated   bool
	Body            string
	RawEnvelope     map[string]any
}

type AttachmentSummary struct {
	Base64Count int `json:"base64_count"`
	ApproxBytes int `json:"approx_bytes"`
}

type PromptAuditLog struct {
	ID                  int64             `json:"id"`
	CreatedAt           time.Time         `json:"created_at"`
	SourceTimestamp     time.Time         `json:"source_timestamp"`
	RedisMessageID      string            `json:"redis_message_id"`
	ClientRequestID     string            `json:"client_request_id"`
	RequestID           string            `json:"request_id,omitempty"`
	UserID              *int64            `json:"user_id,omitempty"`
	APIKeyID            *int64            `json:"api_key_id,omitempty"`
	GroupID             *int64            `json:"group_id,omitempty"`
	Endpoint            string            `json:"endpoint"`
	Model               string            `json:"model"`
	Stream              bool              `json:"stream"`
	Transport           string            `json:"transport"`
	WSTurn              int               `json:"ws_turn"`
	SampleReason        string            `json:"sample_reason"`
	BodyBytes           int               `json:"body_bytes"`
	BodyTruncated       bool              `json:"body_truncated"`
	Body                string            `json:"body"`
	SystemText          string            `json:"system_text,omitempty"`
	ConversationExcerpt string            `json:"conversation_excerpt,omitempty"`
	ToolSummary         []string          `json:"tool_summary"`
	AttachmentSummary   AttachmentSummary `json:"attachment_summary"`
	RiskTags            []string          `json:"risk_tags"`
	RawEnvelope         map[string]any    `json:"raw_envelope"`
}

type ListLogsFilter struct {
	UserID       *int64
	APIKeyID     *int64
	GroupID      *int64
	Model        string
	Transport    string
	SampleReason string
	RiskTag      string
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
}

type PromptAuditStats struct {
	TotalLogs   int64      `json:"total_logs"`
	HTTPLogs    int64      `json:"http_logs"`
	WSLogs      int64      `json:"ws_logs"`
	ForcedLogs  int64      `json:"forced_logs"`
	UniqueUsers int64      `json:"unique_users"`
	LatestAt    *time.Time `json:"latest_at,omitempty"`
}

type RuntimeConfig struct {
	ConsumerPaused bool      `json:"consumer_paused"`
	KeywordRules   []string  `json:"keyword_rules"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ConsumerStatus struct {
	Running         bool       `json:"running"`
	Paused          bool       `json:"paused"`
	LastMessageID   string     `json:"last_message_id,omitempty"`
	LastProcessedAt *time.Time `json:"last_processed_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ProcessedCount  int64      `json:"processed_count"`
	InsertedCount   int64      `json:"inserted_count"`
	AckedCount      int64      `json:"acked_count"`
}
