package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
)

var ErrSettingNotFound = errors.New("setting not found")

type SettingRecord struct {
	Value     string
	UpdatedAt time.Time
}

type SettingsRepository struct {
	db *sql.DB
}

func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

func (r *SettingsRepository) Get(ctx context.Context, key string) (*SettingRecord, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrSettingNotFound
	}

	row := r.db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = $1 LIMIT 1`, key)
	record := &SettingRecord{}
	if err := row.Scan(&record.Value, &record.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSettingNotFound
		}
		return nil, err
	}
	return record, nil
}

func (r *SettingsRepository) GetValue(ctx context.Context, key string) (string, error) {
	record, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return record.Value, nil
}

func (r *SettingsRepository) SetValue(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrSettingNotFound
	}

	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO settings (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key)
		 DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key,
		value,
	)
	return err
}

func (r *SettingsRepository) GetAdminAPIKey(ctx context.Context) (string, error) {
	value, err := r.GetValue(ctx, model.SettingKeyAdminAPIKey)
	if errors.Is(err, ErrSettingNotFound) {
		return "", nil
	}
	return value, err
}

func (r *SettingsRepository) GetRuntimeConfig(ctx context.Context, defaultKeywords []string) (model.RuntimeConfig, error) {
	record, err := r.Get(ctx, model.SettingKeySidecarPromptConfig)
	if errors.Is(err, ErrSettingNotFound) {
		return model.RuntimeConfig{
			KeywordRules: normalizeKeywords(defaultKeywords),
		}, nil
	}
	if err != nil {
		return model.RuntimeConfig{}, err
	}

	cfg := model.RuntimeConfig{}
	if unmarshalErr := json.Unmarshal([]byte(record.Value), &cfg); unmarshalErr != nil {
		return model.RuntimeConfig{
			KeywordRules: normalizeKeywords(defaultKeywords),
			UpdatedAt:    record.UpdatedAt,
		}, nil
	}
	cfg.KeywordRules = normalizeKeywords(cfg.KeywordRules)
	cfg.UpdatedAt = record.UpdatedAt
	if len(cfg.KeywordRules) == 0 {
		cfg.KeywordRules = normalizeKeywords(defaultKeywords)
	}
	return cfg, nil
}

func (r *SettingsRepository) SetRuntimeConfig(ctx context.Context, cfg model.RuntimeConfig) (model.RuntimeConfig, error) {
	cfg.KeywordRules = normalizeKeywords(cfg.KeywordRules)
	payload, err := json.Marshal(struct {
		ConsumerPaused bool     `json:"consumer_paused"`
		KeywordRules   []string `json:"keyword_rules"`
	}{
		ConsumerPaused: cfg.ConsumerPaused,
		KeywordRules:   cfg.KeywordRules,
	})
	if err != nil {
		return model.RuntimeConfig{}, err
	}

	if err := r.SetValue(ctx, model.SettingKeySidecarPromptConfig, string(payload)); err != nil {
		return model.RuntimeConfig{}, err
	}

	stored, err := r.GetRuntimeConfig(ctx, cfg.KeywordRules)
	if err != nil {
		return model.RuntimeConfig{}, err
	}
	return stored, nil
}

func normalizeKeywords(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.TrimSpace(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
