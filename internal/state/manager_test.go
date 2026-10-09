package state

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"baccarat-live-simulator/internal/model"
	"baccarat-live-simulator/internal/sim"
	"baccarat-live-simulator/internal/store"
)

func TestPlaySequencesStatsAndHistoryWindow(t *testing.T) {
	m := New(store.NewMemory(), nil)
	m.historyLimit = 2
	m.now = func() time.Time { return time.Date(2026, 10, 9, 5, 0, 0, 0, time.UTC) }
	m.random = &scriptReader{values: []uint64{0, 0, uint64(sim.WeightPlayer), uint64(sim.WeightPlayer + sim.WeightBanker)}}
	m.mu.Lock()
	m.status = model.StatusRunning
	m.loopCtx = context.Background()
	m.loopGen = 7
	m.mu.Unlock()

	for i := 0; i < 4; i++ {
		if err := m.play(context.Background(), 7); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.V != 1 || snap.State.Stats.Total != 4 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.State.Stats.Counts.Player != 2 || snap.State.Stats.Counts.Banker != 1 || snap.State.Stats.Counts.Tie != 1 {
		t.Fatalf("counts = %+v", snap.State.Stats.Counts)
	}
	if len(snap.State.Rounds) != 2 || snap.State.Rounds[0].ID != 3 || snap.State.Rounds[1].ID != 4 {
		t.Fatalf("window = %+v", snap.State.Rounds)
	}
	data, err := m.repo.Load(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.NextRoundID != 5 || data.Rounds[0].ID != 1 || data.Rounds[3].ID != 4 {
		t.Fatalf("stored = %+v", data)
	}
	if snap.State.Percentages.Player != 50 || snap.State.Percentages.Banker != 25 || snap.State.Percentages.Tie != 25 {
		t.Fatalf("percentages = %+v", snap.State.Percentages)
	}
}

func TestStartPauseResumeAndDuplicateStart(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	m := New(mem, nil)
	m.speedMS = 10000
	m.random = constReader{value: 0}
	pub := &recordPub{}
	m.SetPublisher(pub)

	if _, err := m.Pause(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("pause stopped: %v", err)
	}
	if _, err := m.Resume(ctx); !errors.Is(err, ErrNotPaused) {
		t.Fatalf("resume stopped: %v", err)
	}

	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRounds(t, m, 1)
	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m.loopStarts.Load(); got != 1 {
		t.Fatalf("loop starts = %d, want 1", got)
	}

	if _, err := m.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx); !errors.Is(err, ErrPaused) {
		t.Fatalf("start while paused: %v", err)
	}
	if got := m.loopStarts.Load(); got != 1 {
		t.Fatalf("paused start created a loop: %d", got)
	}
	time.Sleep(150 * time.Millisecond)
	if total := mustTotal(t, m); total != 1 {
		t.Fatalf("rounds while paused = %d", total)
	}

	if _, err := m.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRounds(t, m, 2)
	if got := m.loopStarts.Load(); got != 1 {
		t.Fatalf("resume started a second loop: %d", got)
	}

	env, err := m.Reset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if env.State.Status != model.StatusStopped || env.State.Stats.Total != 0 || len(env.State.Rounds) != 0 {
		t.Fatalf("reset env = %+v", env.State)
	}
	if got := m.loopStarts.Load(); got != 1 {
		t.Fatalf("loop starts after reset = %d", got)
	}

	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRounds(t, m, 1)
	data, err := mem.Load(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.Rounds[0].ID != 1 || data.NextRoundID != 2 {
		t.Fatalf("ids after reset = %+v", data)
	}
	m.Stop()
	if pub.count() == 0 {
		t.Fatal("expected published events")
	}
}

func TestStaleWakeAfterResetDoesNotSkipTheInterval(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemory()
	m := New(mem, nil)
	m.speedMS = 10000
	m.random = constReader{value: 0}

	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRounds(t, m, 1)
	if _, err := m.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	// Reset cancels the loop and also pokes it. If the loop observes
	// cancellation first, that poke stays buffered. The next loop must not
	// treat it as permission to skip the speed interval.
	m.wake <- struct{}{}

	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRounds(t, m, 1)
	time.Sleep(200 * time.Millisecond)

	data, err := mem.Load(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.Stats.Total != 1 || data.NextRoundID != 2 || len(data.Rounds) != 1 || data.Rounds[0].ID != 1 {
		t.Fatalf("restart after stale wake = %+v", data)
	}
	m.Stop()
}

func TestBlockingStartDoesNotDuplicateTheLoop(t *testing.T) {
	ctx := context.Background()
	gate := &gateRepo{
		Memory:  store.NewMemory(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	m := New(gate, nil)
	m.speedMS = 10000
	m.random = constReader{value: 0}

	done := make(chan error, 1)
	go func() {
		_, err := m.Start(ctx)
		done <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first round")
	}
	if _, err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := m.loopStarts.Load(); got != 1 {
		t.Fatalf("loop starts = %d", got)
	}
	close(gate.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start did not return")
	}
	m.Stop()
}

func TestSetSpeedValidation(t *testing.T) {
	m := New(store.NewMemory(), nil)
	if _, err := m.SetSpeed(context.Background(), 10); !errors.Is(err, ErrInvalidSpeed) {
		t.Fatalf("got %v", err)
	}
	env, err := m.SetSpeed(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	if env.State.SpeedMS != 500 || env.Seq != 1 || env.Type != EventStatus {
		t.Fatalf("env = %+v", env)
	}
}

func TestConcurrentControls(t *testing.T) {
	ctx := context.Background()
	m := New(store.NewMemory(), nil)
	m.speedMS = 200
	m.random = constReader{value: uint64(sim.WeightPlayer)}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 12; j++ {
				_, _ = m.Start(ctx)
				_, _ = m.Pause(ctx)
				_, _ = m.Resume(ctx)
				_, _ = m.Snapshot(ctx)
				_, _ = m.SetSpeed(ctx, 500)
				_, _ = m.SetSpeed(ctx, model.DefaultSpeedMS)
			}
		}()
	}
	wg.Wait()
	if _, err := m.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.State.Stats.Total != 0 || snap.State.Status != model.StatusStopped {
		t.Fatalf("after reset = %+v", snap.State)
	}
	m.Stop()
}

func TestSequentialRoundIDs(t *testing.T) {
	m := New(store.NewMemory(), nil)
	m.random = constReader{value: uint64(sim.WeightPlayer + sim.WeightBanker)}
	m.mu.Lock()
	m.status = model.StatusRunning
	m.loopCtx = context.Background()
	m.loopGen = 1
	m.mu.Unlock()
	for i := 1; i <= 6; i++ {
		if err := m.play(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	data, err := m.repo.Load(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for i, round := range data.Rounds {
		if round.ID != int64(i+1) || round.Outcome != model.Tie {
			t.Fatalf("round %d = %+v", i, round)
		}
	}
}

type recordPub struct {
	mu     sync.Mutex
	events []Envelope
}

func (r *recordPub) Publish(env Envelope) {
	r.mu.Lock()
	r.events = append(r.events, env)
	r.mu.Unlock()
}

func (r *recordPub) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

type gateRepo struct {
	*store.Memory
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gateRepo) ApplyRound(ctx context.Context, outcome model.Outcome, at time.Time) (model.Round, model.Stats, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return model.Round{}, model.Stats{}, ctx.Err()
	}
	return g.Memory.ApplyRound(ctx, outcome, at)
}

type constReader struct{ value uint64 }

func (c constReader) Read(p []byte) (int, error) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], c.value)
	n := copy(p, buf[:])
	if n < len(p) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

type scriptReader struct {
	values []uint64
	i      int
}

func (s *scriptReader) Read(p []byte) (int, error) {
	if s.i >= len(s.values) {
		return 0, io.EOF
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], s.values[s.i])
	s.i++
	n := copy(p, buf[:])
	return n, nil
}

func waitForRounds(t *testing.T, m *Manager, total int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mustTotal(t, m) >= total {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d rounds, have %d", total, mustTotal(t, m))
}

func mustTotal(t *testing.T, m *Manager) int64 {
	t.Helper()
	snap, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snap.State.Stats.Total
}
