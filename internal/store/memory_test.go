package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"baccarat-live-simulator/internal/model"
)

func TestMemorySequenceStatsAndReset(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	now := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	outcomes := []model.Outcome{model.Player, model.Player, model.Banker, model.Tie}
	for i, outcome := range outcomes {
		round, stats, err := mem.ApplyRound(ctx, outcome, now.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if round.ID != int64(i+1) {
			t.Fatalf("id = %d", round.ID)
		}
		if stats.Total != int64(i+1) {
			t.Fatalf("total = %d", stats.Total)
		}
	}
	data, err := mem.Load(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if data.NextRoundID != 5 || data.Stats.Counts.Player != 2 || data.Stats.Counts.Banker != 1 || data.Stats.Counts.Tie != 1 {
		t.Fatalf("data = %+v", data)
	}
	if len(data.Rounds) != 2 || data.Rounds[0].ID != 3 || data.Rounds[1].ID != 4 {
		t.Fatalf("window = %+v", data.Rounds)
	}
	newest, err := mem.ListRounds(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if newest[0].ID != 4 || newest[1].ID != 3 {
		t.Fatalf("newest = %+v", newest)
	}
	if err := mem.SetSpeed(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if err := mem.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	data, err = mem.Load(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.NextRoundID != 1 || data.Stats.Total != 0 || len(data.Rounds) != 0 || data.SpeedMS != 500 {
		t.Fatalf("after reset = %+v", data)
	}
	if _, _, err := mem.ApplyRound(ctx, model.Outcome("void"), now); err == nil {
		t.Fatal("expected invalid outcome")
	}
}

func TestMemoryConcurrentIDsAreUnique(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	const n = 40
	var wg sync.WaitGroup
	ids := make(chan int64, n)
	errCh := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			round, _, err := mem.ApplyRound(ctx, model.Banker, time.Now().UTC())
			if err != nil {
				errCh <- err
				return
			}
			ids <- round.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d ids", len(seen))
	}
	data, err := mem.Load(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if data.NextRoundID != int64(n+1) || data.Stats.Total != int64(n) {
		t.Fatalf("data = %+v", data)
	}
}
