package consumer

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api-audit/internal/model"
)

type StatusStore struct {
	mu     sync.RWMutex
	status model.ConsumerStatus
}

func NewStatusStore() *StatusStore {
	return &StatusStore{}
}

func (s *StatusStore) Snapshot() model.ConsumerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.status
	if snapshot.LastProcessedAt != nil {
		t := *snapshot.LastProcessedAt
		snapshot.LastProcessedAt = &t
	}
	return snapshot
}

func (s *StatusStore) SetRunning(running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = running
}

func (s *StatusStore) SetPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Paused = paused
}

func (s *StatusStore) RecordProcessed(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.status.LastMessageID = messageID
	s.status.LastProcessedAt = &now
	s.status.ProcessedCount++
	s.status.LastError = ""
}

func (s *StatusStore) RecordInserted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.InsertedCount++
}

func (s *StatusStore) RecordAcked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.AckedCount++
}

func (s *StatusStore) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastError = err.Error()
}
