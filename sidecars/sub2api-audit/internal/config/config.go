package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Audit     AuditConfig     `mapstructure:"audit"`
	AdminAuth AdminAuthConfig `mapstructure:"admin_auth"`
}

type ServerConfig struct {
	Host                     string `mapstructure:"host"`
	Port                     int    `mapstructure:"port"`
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"`
	IdleTimeoutSeconds       int    `mapstructure:"idle_timeout_seconds"`
}

type DatabaseConfig struct {
	DSN                    string `mapstructure:"dsn"`
	MaxOpenConns           int    `mapstructure:"max_open_conns"`
	MaxIdleConns           int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetimeMinutes int    `mapstructure:"conn_max_lifetime_minutes"`
}

type RedisConfig struct {
	Addr                string `mapstructure:"addr"`
	Password            string `mapstructure:"password"`
	DB                  int    `mapstructure:"db"`
	DialTimeoutSeconds  int    `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds  int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `mapstructure:"write_timeout_seconds"`
}

type AuditConfig struct {
	ConsumerEnabled      bool     `mapstructure:"consumer_enabled"`
	StreamName           string   `mapstructure:"stream_name"`
	ConsumerGroup        string   `mapstructure:"consumer_group"`
	ConsumerName         string   `mapstructure:"consumer_name"`
	BatchSize            int64    `mapstructure:"batch_size"`
	BlockSeconds         int      `mapstructure:"block_seconds"`
	InsertTimeoutSeconds int      `mapstructure:"insert_timeout_seconds"`
	DefaultListLimit     int      `mapstructure:"default_list_limit"`
	MaxListLimit         int      `mapstructure:"max_list_limit"`
	KeywordRules         []string `mapstructure:"keyword_rules"`
}

type AdminAuthConfig struct {
	AdminAPIKey string `mapstructure:"admin_api_key"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetEnvPrefix("SUB2API_AUDIT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if strings.TrimSpace(configPath) != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		addSearchPaths(v)
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && strings.TrimSpace(configPath) != "" {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if strings.TrimSpace(cfg.Audit.ConsumerName) == "" {
		cfg.Audit.ConsumerName = defaultConsumerName()
	}
	cfg.Audit.KeywordRules = normalizeKeywords(cfg.Audit.KeywordRules)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Server.Host) == "" {
		return fmt.Errorf("server.host is required")
	}
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive")
	}
	if c.Server.ReadHeaderTimeoutSeconds <= 0 {
		return fmt.Errorf("server.read_header_timeout_seconds must be positive")
	}
	if c.Server.IdleTimeoutSeconds <= 0 {
		return fmt.Errorf("server.idle_timeout_seconds must be positive")
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Database.MaxOpenConns <= 0 {
		return fmt.Errorf("database.max_open_conns must be positive")
	}
	if c.Database.MaxIdleConns < 0 {
		return fmt.Errorf("database.max_idle_conns must be non-negative")
	}
	if c.Database.ConnMaxLifetimeMinutes <= 0 {
		return fmt.Errorf("database.conn_max_lifetime_minutes must be positive")
	}
	if strings.TrimSpace(c.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if c.Redis.DialTimeoutSeconds <= 0 || c.Redis.ReadTimeoutSeconds <= 0 || c.Redis.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("redis timeouts must be positive")
	}
	if strings.TrimSpace(c.Audit.StreamName) == "" {
		return fmt.Errorf("audit.stream_name is required")
	}
	if strings.TrimSpace(c.Audit.ConsumerGroup) == "" {
		return fmt.Errorf("audit.consumer_group is required")
	}
	if strings.TrimSpace(c.Audit.ConsumerName) == "" {
		return fmt.Errorf("audit.consumer_name is required")
	}
	if c.Audit.BatchSize <= 0 {
		return fmt.Errorf("audit.batch_size must be positive")
	}
	if c.Audit.BlockSeconds <= 0 {
		return fmt.Errorf("audit.block_seconds must be positive")
	}
	if c.Audit.InsertTimeoutSeconds <= 0 {
		return fmt.Errorf("audit.insert_timeout_seconds must be positive")
	}
	if c.Audit.DefaultListLimit <= 0 {
		return fmt.Errorf("audit.default_list_limit must be positive")
	}
	if c.Audit.MaxListLimit < c.Audit.DefaultListLimit {
		return fmt.Errorf("audit.max_list_limit must be >= audit.default_list_limit")
	}
	return nil
}

func (c *Config) ServerAddr() string {
	return c.Server.Host + ":" + strconv.Itoa(c.Server.Port)
}

func (c *Config) RedisOptions() *redis.Options {
	return &redis.Options{
		Addr:         c.Redis.Addr,
		Password:     c.Redis.Password,
		DB:           c.Redis.DB,
		DialTimeout:  secondsDuration(c.Redis.DialTimeoutSeconds),
		ReadTimeout:  secondsDuration(c.Redis.ReadTimeoutSeconds),
		WriteTimeout: secondsDuration(c.Redis.WriteTimeoutSeconds),
	}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8087)
	v.SetDefault("server.read_header_timeout_seconds", 10)
	v.SetDefault("server.idle_timeout_seconds", 120)

	v.SetDefault("database.max_open_conns", 10)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime_minutes", 30)

	v.SetDefault("redis.addr", "127.0.0.1:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.dial_timeout_seconds", 5)
	v.SetDefault("redis.read_timeout_seconds", 5)
	v.SetDefault("redis.write_timeout_seconds", 5)

	v.SetDefault("audit.consumer_enabled", true)
	v.SetDefault("audit.stream_name", "prompt_audit:sampled")
	v.SetDefault("audit.consumer_group", "prompt-audit-sidecar")
	v.SetDefault("audit.batch_size", 16)
	v.SetDefault("audit.block_seconds", 5)
	v.SetDefault("audit.insert_timeout_seconds", 5)
	v.SetDefault("audit.default_list_limit", 50)
	v.SetDefault("audit.max_list_limit", 200)
	v.SetDefault("audit.keyword_rules", []string{})

	v.SetDefault("admin_auth.admin_api_key", "")
}

func addSearchPaths(v *viper.Viper) {
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		v.AddConfigPath(dataDir)
	}
	v.AddConfigPath("/app/data")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/sub2api-audit")
	v.AddConfigPath("/etc/sub2api")
}

func defaultConsumerName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "localhost"
	}
	return strings.ReplaceAll(host, string(filepath.Separator), "-") + "-audit"
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

func secondsDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}
