// Package store persists simulated rounds and the speed setting.
package store

import (
	"context"
	"errors"
	"time"

	"baccarat-live-simulator/internal/model"
)

// ErrInvalidOutcome is returned when a round outcome is not recognised.
var ErrInvalidOutcome = model.ErrInvalidOutcome

// Data is the persisted portion of a snapshot.
// Rounds are chronological, oldest first, and limited to the requested window.
type Data struct {
	SpeedMS     int
	NextRoundID int64
	Stats       model.Stats
	Rounds      []model.Round
}

// Repository is the persistence contract used by the simulation manager.
type Repository interface {
	Ping(ctx context.Context) error
	Load(ctx context.Context, historyLimit int) (Data, error)
	ApplyRound(ctx context.Context, outcome model.Outcome, at time.Time) (model.Round, model.Stats, error)
	ListRounds(ctx context.Context, limit int) ([]model.Round, error)
	SetSpeed(ctx context.Context, speedMS int) error
	// Reset deletes simulated rounds and restarts round IDs at 1.
	// It does not drop tables or touch anything outside the simulator schema.
	Reset(ctx context.Context) error
}

// ErrDuplicateRound marks an insert that collided with an existing round ID.
var ErrDuplicateRound = errors.New("duplicate round")
