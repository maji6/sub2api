package consumer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/audit"
	"github.com/Wei-Shaw/sub2api-audit/internal/config"
	"github.com/Wei-Shaw/sub2api-audit/internal/model"
	"github.com/Wei-Shaw/sub2api-audit/internal/repository"
	"github.com/redis/go-redis/v9"
)

type StreamConsumer struct {
	redisClient  *redis.Client
	auditRepo    *repository.PromptAuditLogRepository
	settingsRepo *repository.SettingsRepository
	status       *StatusStore
	cfg          config.AuditConfig
}

func NewStreamConsumer(
	redisClient *redis.Client,
	auditRepo *repository.PromptAuditLogRepository,
	settingsRepo *repository.SettingsRepository,
	status *StatusStore,
	cfg config.AuditConfig,
) *StreamConsumer {
	return &StreamConsumer{
		redisClient:  redisClient,
		auditRepo:    auditRepo,
		settingsRepo: settingsRepo,
		status:       status,
		cfg:          cfg,
	}
}

func (c *StreamConsumer) Run(ctx context.Context) error {
	c.status.SetRunning(true)
	defer c.status.SetRunning(false)

	if err := c.ensureConsumerGroup(ctx); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		runtimeConfig, err := c.settingsRepo.GetRuntimeConfig(ctx, c.cfg.KeywordRules)
		if err != nil {
			c.status.RecordError(err)
			log.Printf("load runtime config: %v", err)
			if err := sleepContext(ctx, time.Second); err != nil {
				return err
			}
			continue
		}

		c.status.SetPaused(runtimeConfig.ConsumerPaused)
		if runtimeConfig.ConsumerPaused {
			if err := sleepContext(ctx, 2*time.Second); err != nil {
				return err
			}
			continue
		}

		streams, err := c.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.cfg.ConsumerGroup,
			Consumer: c.cfg.ConsumerName,
			Streams:  []string{c.cfg.StreamName, ">"},
			Count:    c.cfg.BatchSize,
			Block:    time.Duration(c.cfg.BlockSeconds) * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.status.RecordError(err)
			log.Printf("xreadgroup failed: %v", err)
			if err := sleepContext(ctx, time.Second); err != nil {
				return err
			}
			continue
		}

		for _, stream := range streams {
			for _, message := range stream.Messages {
				if err := c.processMessage(ctx, message, runtimeConfig); err != nil {
					c.status.RecordError(err)
					log.Printf("process message %s failed: %v", message.ID, err)
				}
			}
		}
	}
}

func (c *StreamConsumer) ensureConsumerGroup(ctx context.Context) error {
	err := c.redisClient.XGroupCreateMkStream(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, "0").Err()
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("create consumer group: %w", err)
}

func (c *StreamConsumer) processMessage(ctx context.Context, message redis.XMessage, runtimeConfig model.RuntimeConfig) error {
	envelope, err := ParseEnvelope(message)
	if err != nil {
		_ = c.redisClient.XAck(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, message.ID).Err()
		return fmt.Errorf("parse envelope: %w", err)
	}

	item := audit.BuildPromptAuditLog(envelope, runtimeConfig)
	insertCtx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.InsertTimeoutSeconds)*time.Second)
	defer cancel()

	if err := c.auditRepo.Insert(insertCtx, &item); err != nil {
		return fmt.Errorf("insert prompt audit log: %w", err)
	}

	c.status.RecordProcessed(message.ID)
	c.status.RecordInserted()

	if err := c.redisClient.XAck(ctx, c.cfg.StreamName, c.cfg.ConsumerGroup, message.ID).Err(); err != nil {
		return fmt.Errorf("ack redis stream message: %w", err)
	}
	c.status.RecordAcked()
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
