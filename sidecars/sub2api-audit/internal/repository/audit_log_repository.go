package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
)

type PromptAuditLogRepository struct {
	db *sql.DB
}

func NewPromptAuditLogRepository(db *sql.DB) *PromptAuditLogRepository {
	return &PromptAuditLogRepository{db: db}
}

func (r *PromptAuditLogRepository) Insert(ctx context.Context, item *model.PromptAuditLog) error {
	if item == nil {
		return fmt.Errorf("prompt audit log is nil")
	}

	toolSummaryJSON, err := json.Marshal(item.ToolSummary)
	if err != nil {
		return fmt.Errorf("marshal tool summary: %w", err)
	}
	attachmentSummaryJSON, err := json.Marshal(item.AttachmentSummary)
	if err != nil {
		return fmt.Errorf("marshal attachment summary: %w", err)
	}
	riskTagsJSON, err := json.Marshal(item.RiskTags)
	if err != nil {
		return fmt.Errorf("marshal risk tags: %w", err)
	}
	rawEnvelope := item.RawEnvelope
	if rawEnvelope == nil {
		rawEnvelope = map[string]any{}
	}
	rawEnvelopeJSON, err := json.Marshal(rawEnvelope)
	if err != nil {
		return fmt.Errorf("marshal raw envelope: %w", err)
	}

	_, err = r.db.ExecContext(
		ctx,
		`INSERT INTO prompt_audit_logs (
			source_timestamp, redis_message_id, client_request_id, request_id, user_id, api_key_id, group_id,
			endpoint, model, stream, transport, ws_turn, sample_reason, body_bytes, body_truncated, body,
			system_text, conversation_excerpt, tool_summary, attachment_summary, risk_tags, raw_envelope
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22
		)`,
		item.SourceTimestamp,
		item.RedisMessageID,
		item.ClientRequestID,
		nullableString(item.RequestID),
		item.UserID,
		item.APIKeyID,
		item.GroupID,
		item.Endpoint,
		item.Model,
		item.Stream,
		item.Transport,
		item.WSTurn,
		item.SampleReason,
		item.BodyBytes,
		item.BodyTruncated,
		item.Body,
		item.SystemText,
		item.ConversationExcerpt,
		toolSummaryJSON,
		attachmentSummaryJSON,
		riskTagsJSON,
		rawEnvelopeJSON,
	)
	return err
}

func (r *PromptAuditLogRepository) List(ctx context.Context, filter model.ListLogsFilter) ([]model.PromptAuditLog, int64, error) {
	whereSQL, args := buildAuditWhere(filter)

	var total int64
	countQuery := `SELECT COUNT(*) FROM prompt_audit_logs WHERE ` + whereSQL
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, filter.Limit, filter.Offset)
	listQuery := `SELECT
		id, created_at, source_timestamp, redis_message_id, client_request_id, request_id,
		user_id, api_key_id, group_id, endpoint, model, stream, transport, ws_turn, sample_reason,
		body_bytes, body_truncated, body, system_text, conversation_excerpt, tool_summary,
		attachment_summary, risk_tags, raw_envelope
		FROM prompt_audit_logs
		WHERE ` + whereSQL + `
		ORDER BY created_at DESC
		LIMIT $` + fmt.Sprintf("%d", len(args)-1) + ` OFFSET $` + fmt.Sprintf("%d", len(args))

	rows, err := r.db.QueryContext(ctx, listQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]model.PromptAuditLog, 0, filter.Limit)
	for rows.Next() {
		item, scanErr := scanPromptAuditLog(rows)
		if scanErr != nil {
			return nil, 0, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *PromptAuditLogRepository) Stats(ctx context.Context, filter model.ListLogsFilter) (model.PromptAuditStats, error) {
	whereSQL, args := buildAuditWhere(filter)
	query := `SELECT
		COUNT(*) AS total_logs,
		COUNT(*) FILTER (WHERE transport = 'http') AS http_logs,
		COUNT(*) FILTER (WHERE transport = 'ws') AS ws_logs,
		COUNT(*) FILTER (WHERE sample_reason <> 'random') AS forced_logs,
		COUNT(DISTINCT user_id) AS unique_users,
		MAX(source_timestamp) AS latest_at
		FROM prompt_audit_logs
		WHERE ` + whereSQL

	var stats model.PromptAuditStats
	var latestAt sql.NullTime
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&stats.TotalLogs,
		&stats.HTTPLogs,
		&stats.WSLogs,
		&stats.ForcedLogs,
		&stats.UniqueUsers,
		&latestAt,
	); err != nil {
		return model.PromptAuditStats{}, err
	}
	if latestAt.Valid {
		t := latestAt.Time
		stats.LatestAt = &t
	}
	return stats, nil
}

func buildAuditWhere(filter model.ListLogsFilter) (string, []any) {
	clauses := []string{"TRUE"}
	args := make([]any, 0, 8)

	addClause := func(format string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(format, len(args)))
	}

	if filter.UserID != nil {
		addClause("user_id = $%d", *filter.UserID)
	}
	if filter.APIKeyID != nil {
		addClause("api_key_id = $%d", *filter.APIKeyID)
	}
	if filter.GroupID != nil {
		addClause("group_id = $%d", *filter.GroupID)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		addClause("model = $%d", value)
	}
	if value := strings.TrimSpace(filter.Transport); value != "" {
		addClause("transport = $%d", value)
	}
	if value := strings.TrimSpace(filter.SampleReason); value != "" {
		addClause("sample_reason = $%d", value)
	}
	if value := strings.TrimSpace(filter.RiskTag); value != "" {
		addClause("risk_tags ? $%d", value)
	}
	if filter.From != nil {
		addClause("created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		addClause("created_at < $%d", *filter.To)
	}

	return strings.Join(clauses, " AND "), args
}

func scanPromptAuditLog(scanner interface {
	Scan(dest ...any) error
}) (model.PromptAuditLog, error) {
	var item model.PromptAuditLog
	var requestID sql.NullString
	var userID sql.NullInt64
	var apiKeyID sql.NullInt64
	var groupID sql.NullInt64
	var toolSummaryJSON []byte
	var attachmentSummaryJSON []byte
	var riskTagsJSON []byte
	var rawEnvelopeJSON []byte

	if err := scanner.Scan(
		&item.ID,
		&item.CreatedAt,
		&item.SourceTimestamp,
		&item.RedisMessageID,
		&item.ClientRequestID,
		&requestID,
		&userID,
		&apiKeyID,
		&groupID,
		&item.Endpoint,
		&item.Model,
		&item.Stream,
		&item.Transport,
		&item.WSTurn,
		&item.SampleReason,
		&item.BodyBytes,
		&item.BodyTruncated,
		&item.Body,
		&item.SystemText,
		&item.ConversationExcerpt,
		&toolSummaryJSON,
		&attachmentSummaryJSON,
		&riskTagsJSON,
		&rawEnvelopeJSON,
	); err != nil {
		return model.PromptAuditLog{}, err
	}

	if requestID.Valid {
		item.RequestID = requestID.String
	}
	if userID.Valid {
		value := userID.Int64
		item.UserID = &value
	}
	if apiKeyID.Valid {
		value := apiKeyID.Int64
		item.APIKeyID = &value
	}
	if groupID.Valid {
		value := groupID.Int64
		item.GroupID = &value
	}

	if len(toolSummaryJSON) > 0 {
		_ = json.Unmarshal(toolSummaryJSON, &item.ToolSummary)
	}
	if len(attachmentSummaryJSON) > 0 {
		_ = json.Unmarshal(attachmentSummaryJSON, &item.AttachmentSummary)
	}
	if len(riskTagsJSON) > 0 {
		_ = json.Unmarshal(riskTagsJSON, &item.RiskTags)
	}
	if len(rawEnvelopeJSON) > 0 {
		_ = json.Unmarshal(rawEnvelopeJSON, &item.RawEnvelope)
	}
	return item, nil
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
