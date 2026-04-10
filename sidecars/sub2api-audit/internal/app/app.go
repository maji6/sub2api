package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	_ "github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api-audit/internal/config"
	"github.com/Wei-Shaw/sub2api-audit/internal/consumer"
	audithttp "github.com/Wei-Shaw/sub2api-audit/internal/http"
	"github.com/Wei-Shaw/sub2api-audit/internal/migrate"
	"github.com/Wei-Shaw/sub2api-audit/internal/repository"
	"github.com/redis/go-redis/v9"
)

type App struct {
	Config   *config.Config
	DB       *sql.DB
	Redis    *redis.Client
	Server   *http.Server
	Consumer *consumer.StreamConsumer
}

func New(ctx context.Context, cfg *config.Config) (*App, error) {
	db, err := sql.Open("postgres", cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLifetimeMinutes) * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := migrate.Run(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	redisClient := redis.NewClient(cfg.RedisOptions())
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		_ = db.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	settingsRepo := repository.NewSettingsRepository(db)
	auditRepo := repository.NewPromptAuditLogRepository(db)
	statusStore := consumer.NewStatusStore()

	router := audithttp.NewRouter(audithttp.Dependencies{
		Config:       cfg,
		SettingsRepo: settingsRepo,
		AuditRepo:    auditRepo,
		StatusStore:  statusStore,
	})

	server := &http.Server{
		Addr:              cfg.ServerAddr(),
		Handler:           router,
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadHeaderTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	return &App{
		Config: cfg,
		DB:     db,
		Redis:  redisClient,
		Server: server,
		Consumer: consumer.NewStreamConsumer(
			redisClient,
			auditRepo,
			settingsRepo,
			statusStore,
			cfg.Audit,
		),
	}, nil
}

func (a *App) Close() error {
	var firstErr error
	if a.Redis != nil {
		if err := a.Redis.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.DB != nil {
		if err := a.DB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
