// Package model holds the simulator's shared data types.
package model

import (
	"math"
	"time"
)

const (
	// Version is the simulator release shown in the UI and health response.
	Version = "0.1.0"
	// ProtocolVersion is the WebSocket and state-envelope version.
	ProtocolVersion = 1

	MinSpeedMS     = 200
	MaxSpeedMS     = 30000
	DefaultSpeedMS = 2000

	// DefaultHistory is how many recent rounds are included in a state snapshot.
	// All-time totals still count every persisted round.
	DefaultHistory = 120
	MaxRoundQuery  = 500
)

const (
	StatusStopped = "stopped"
	StatusRunning = "running"
	StatusPaused  = "paused"
)

// Outcome is a simulated baccarat result.
// These values are display labels for the simplified random model.
// They are not the product of a card shoe.
type Outcome string

const (
	Player Outcome = "player"
	Banker Outcome = "banker"
	Tie    Outcome = "tie"
)

// Valid reports whether o is one of the three simulated outcomes.
func (o Outcome) Valid() bool {
	return o == Player || o == Banker || o == Tie
}

// Round is one completed simulated round.
type Round struct {
	ID        int64     `json:"id"`
	Outcome   Outcome   `json:"outcome"`
	CreatedAt time.Time `json:"createdAt"`
}

// Counts are all-time outcome totals.
type Counts struct {
	Player int64 `json:"player"`
	Banker int64 `json:"banker"`
	Tie    int64 `json:"tie"`
}

// Stats is the all-time tally. Total is the sum of the counts.
type Stats struct {
	Total  int64  `json:"total"`
	Counts Counts `json:"counts"`
}

// Percentages are independently rounded to one decimal place.
// They can sum to 99.9 or 100.1 because each value is rounded on its own.
type Percentages struct {
	Player float64 `json:"player"`
	Banker float64 `json:"banker"`
	Tie    float64 `json:"tie"`
}

// Percentages converts counts into display percentages.
// A zero total yields zeros rather than NaN.
func (s Stats) Percentages() Percentages {
	if s.Total <= 0 {
		return Percentages{}
	}
	scale := 100 / float64(s.Total)
	return Percentages{
		Player: round1(float64(s.Counts.Player) * scale),
		Banker: round1(float64(s.Counts.Banker) * scale),
		Tie:    round1(float64(s.Counts.Tie) * scale),
	}
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// Add returns a copy of the stats including one more outcome.
func (s Stats) Add(o Outcome) (Stats, error) {
	if !o.Valid() {
		return Stats{}, ErrInvalidOutcome
	}
	switch o {
	case Player:
		s.Counts.Player++
	case Banker:
		s.Counts.Banker++
	case Tie:
		s.Counts.Tie++
	}
	s.Total = s.Counts.Player + s.Counts.Banker + s.Counts.Tie
	return s, nil
}

// Normalize recomputes Total from the counts.
func (s *Stats) Normalize() {
	s.Total = s.Counts.Player + s.Counts.Banker + s.Counts.Tie
}
