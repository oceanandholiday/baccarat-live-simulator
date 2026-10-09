package model

import "time"

// State is the authoritative view sent to browsers.
// Rounds are chronological (oldest first) and limited to the recent window.
// Stats and Percentages cover every persisted round, not only that window.
type State struct {
	Status       string       `json:"status"`
	SpeedMS      int          `json:"speedMs"`
	Stats        Stats        `json:"stats"`
	Percentages  Percentages  `json:"percentages"`
	Rounds       []Round      `json:"rounds"`
	HistoryLimit int          `json:"historyLimit"`
	ServerTime   time.Time    `json:"serverTime"`
	LastError    string       `json:"lastError,omitempty"`
	Version      string       `json:"version"`
}

// Prepare fills derived fields and guarantees a JSON array for rounds.
func (s *State) Prepare() {
	s.Stats.Normalize()
	s.Percentages = s.Stats.Percentages()
	if s.Rounds == nil {
		s.Rounds = []Round{}
	}
	if s.Version == "" {
		s.Version = Version
	}
	if s.HistoryLimit == 0 {
		s.HistoryLimit = DefaultHistory
	}
}
