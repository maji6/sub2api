CREATE TABLE IF NOT EXISTS settings (
    id         BIGSERIAL PRIMARY KEY,
    key        VARCHAR(100) NOT NULL UNIQUE,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS prompt_audit_logs (
    id                   BIGSERIAL PRIMARY KEY,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source_timestamp     TIMESTAMPTZ NOT NULL,
    redis_message_id     VARCHAR(128) NOT NULL UNIQUE,
    client_request_id    VARCHAR(128) NOT NULL,
    request_id           VARCHAR(128),
    user_id              BIGINT,
    api_key_id           BIGINT,
    group_id             BIGINT,
    endpoint             VARCHAR(255) NOT NULL,
    model                VARCHAR(255) NOT NULL,
    stream               BOOLEAN NOT NULL DEFAULT FALSE,
    transport            VARCHAR(16) NOT NULL,
    ws_turn              INT NOT NULL DEFAULT 0,
    sample_reason        VARCHAR(64) NOT NULL,
    body_bytes           INT NOT NULL DEFAULT 0,
    body_truncated       BOOLEAN NOT NULL DEFAULT FALSE,
    body                 TEXT NOT NULL,
    system_text          TEXT NOT NULL DEFAULT '',
    conversation_excerpt TEXT NOT NULL DEFAULT '',
    tool_summary         JSONB NOT NULL DEFAULT '[]'::jsonb,
    attachment_summary   JSONB NOT NULL DEFAULT '{}'::jsonb,
    risk_tags            JSONB NOT NULL DEFAULT '[]'::jsonb,
    raw_envelope         JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_created_at ON prompt_audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_source_timestamp ON prompt_audit_logs(source_timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_user_id ON prompt_audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_group_id ON prompt_audit_logs(group_id);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_model ON prompt_audit_logs(model);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_transport ON prompt_audit_logs(transport);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_sample_reason ON prompt_audit_logs(sample_reason);
CREATE INDEX IF NOT EXISTS idx_prompt_audit_logs_risk_tags ON prompt_audit_logs USING GIN (risk_tags);
