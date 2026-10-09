package store

import (
	"context"
	"sync"
	"time"

	"baccarat-live-simulator/internal/model"
)

// Memory is an in-process repository for tests and local wiring checks.
type Memory struct {
	mu          sync.Mutex
	speedMS     int
	nextRoundID int64
	rounds      []model.Round
}

// NewMemory returns an empty repository at round 1.
func NewMemory() *Memory {
	return &Memory{speedMS: model.DefaultSpeedMS, nextRoundID: 1}
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Load(_ context.Context, historyLimit int) (Data, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot(historyLimit), nil
}

func (m *Memory) ApplyRound(_ context.Context, outcome model.Outcome, at time.Time) (model.Round, model.Stats, error) {
	if !outcome.Valid() {
		return model.Round{}, model.Stats{}, ErrInvalidOutcome
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextRoundID
	for _, existing := range m.rounds {
		if existing.ID == id {
			return model.Round{}, model.Stats{}, ErrDuplicateRound
		}
	}
	round := model.Round{ID: id, Outcome: outcome, CreatedAt: at.UTC()}
	m.rounds = append(m.rounds, round)
	m.nextRoundID++
	return round, countRounds(m.rounds), nil
}

func (m *Memory) ListRounds(_ context.Context, limit int) ([]model.Round, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		return []model.Round{}, nil
	}
	n := len(m.rounds)
	if limit > n {
		limit = n
	}
	out := make([]model.Round, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, m.rounds[i])
	}
	return out, nil
}

func (m *Memory) SetSpeed(_ context.Context, speedMS int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.speedMS = speedMS
	return nil
}

func (m *Memory) Reset(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rounds = nil
	m.nextRoundID = 1
	return nil
}

func (m *Memory) snapshot(historyLimit int) Data {
	stats := countRounds(m.rounds)
	window := m.rounds
	if historyLimit > 0 && len(window) > historyLimit {
		window = window[len(window)-historyLimit:]
	}
	rounds := append([]model.Round(nil), window...)
	if rounds == nil {
		rounds = []model.Round{}
	}
	return Data{
		SpeedMS:     m.speedMS,
		NextRoundID: m.nextRoundID,
		Stats:       stats,
		Rounds:      rounds,
	}
}

func countRounds(rounds []model.Round) model.Stats {
	var stats model.Stats
	for _, round := range rounds {
		stats, _ = stats.Add(round.Outcome)
	}
	return stats
}
