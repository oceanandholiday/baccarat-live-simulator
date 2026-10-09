// Package sim draws Player, Banker, and Tie results for the live display.
//
// This is a simplified weighted random model. It does not shuffle or deal a
// shoe, and it does not implement official Baccarat drawing rules, commission,
// or pair side bets. The weights are fixed display parameters. They are in the
// rough neighborhood of commonly quoted eight-deck baccarat outcome shares,
// but they are not a certified probability table.
//
// Each draw uses crypto/rand with rejection sampling so the 10_000-way index
// is unbiased. The mapping is:
//
//	[0, 4462)      player
//	[4462, 9048)   banker
//	[9048, 10000)  tie
package sim

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"baccarat-live-simulator/internal/model"
)

const (
	// WeightScale is the number of equally likely slots in one draw.
	WeightScale = 10000
	// WeightPlayer is the number of slots that land on player.
	WeightPlayer = 4462
	// WeightBanker is the number of slots that land on banker.
	WeightBanker = 4586
	// WeightTie is the number of slots that land on tie.
	WeightTie = 952
)

// Reader is the random source used when Draw is called without an override.
// Tests replace it by calling DrawFrom.
var Reader io.Reader = rand.Reader

// Draw returns one simulated outcome from crypto/rand.
func Draw() (model.Outcome, error) {
	return DrawFrom(Reader)
}

// DrawFrom returns one simulated outcome from r.
func DrawFrom(r io.Reader) (model.Outcome, error) {
	if r == nil {
		return "", errors.New("nil random source")
	}
	for attempt := 0; attempt < 16; attempt++ {
		v, err := readUint64(r)
		if err != nil {
			return "", err
		}
		idx, ok := IndexFromUint64(v)
		if !ok {
			continue
		}
		return OutcomeFromIndex(idx)
	}
	return "", errors.New("random source did not yield an unbiased sample")
}

// IndexFromUint64 maps a random uint64 onto [0, WeightScale).
// The boolean is false when the value must be rejected to avoid modulo bias.
func IndexFromUint64(v uint64) (int, bool) {
	limit := (^uint64(0) / WeightScale) * WeightScale
	if v >= limit {
		return 0, false
	}
	return int(v % WeightScale), true
}

// OutcomeFromIndex maps a slot in [0, WeightScale) to an outcome.
func OutcomeFromIndex(n int) (model.Outcome, error) {
	switch {
	case n < 0 || n >= WeightScale:
		return "", fmt.Errorf("outcome index %d out of range", n)
	case n < WeightPlayer:
		return model.Player, nil
	case n < WeightPlayer+WeightBanker:
		return model.Banker, nil
	default:
		return model.Tie, nil
	}
}

func readUint64(r io.Reader) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, fmt.Errorf("read random bytes: %w", err)
	}
	return binary.BigEndian.Uint64(buf[:]), nil
}
