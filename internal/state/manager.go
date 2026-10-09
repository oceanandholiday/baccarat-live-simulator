// Package state runs one simulation loop and publishes versioned snapshots.
package state

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"baccarat-live-simulator/internal/model"
	"baccarat-live-simulator/internal/redact"
	"baccarat-live-simulator/internal/sim"
	"baccarat-live-simulator/internal/store"
)

const (
	// EventSnapshot replaces client state. It is sent on connect and after reset.
	EventSnapshot = "snapshot"
	// EventRound announces one completed round and includes the full state.
	EventRound = "round"
	// EventStatus announces start, pause, resume, stop, and speed changes.
	EventStatus = "status"
)

var (
	// ErrPaused means start was used while the loop is paused. Call Resume.
	ErrPaused = errors.New("simulation is paused")
	// ErrNotRunning means pause was used while the simulator is stopped.
	ErrNotRunning = errors.New("simulation is not running")
	// ErrNotPaused means resume was used while the simulator is stopped.
	ErrNotPaused = errors.New("simulation is not paused")
	// ErrInvalidSpeed means the requested interval is outside the allowed range.
	ErrInvalidSpeed = errors.New("speed is outside the allowed range")
)

// Envelope is the versioned payload shared by the HTTP state API and WebSocket.
type Envelope struct {
	V     int         `json:"v"`
	Type  string      `json:"type"`
	Seq   int64       `json:"seq"`
	State model.State `json:"state"`
}

// Publisher receives events after the manager has updated its in-memory view.
type Publisher interface {
	Publish(Envelope)
}

// Manager owns simulator status and the single simulation goroutine.
type Manager struct {
	repo         store.Repository
	logger       *slog.Logger
	historyLimit int
	now          func() time.Time
	random       io.Reader

	life sync.Mutex
	mu   sync.Mutex

	status    string
	speedMS   int
	rounds    []model.Round
	stats     model.Stats
	lastError string
	seq       int64
	wake      chan struct{}
	pub       Publisher

	loopAlive  bool
	loopGen    int
	loopCancel context.CancelFunc
	loopCtx    context.Context
	wg         sync.WaitGroup
	loopStarts atomic.Int64
}

// New builds a stopped manager. Call Load before serving traffic.
func New(repo store.Repository, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{
		repo:         repo,
		logger:       logger,
		historyLimit: model.DefaultHistory,
		now:          time.Now,
		random:       sim.Reader,
		status:       model.StatusStopped,
		speedMS:      model.DefaultSpeedMS,
		wake:         make(chan struct{}, 1),
	}
}

// SetPublisher attaches the WebSocket hub. Nil discards events.
func (m *Manager) SetPublisher(pub Publisher) {
	m.mu.Lock()
	m.pub = pub
	m.mu.Unlock()
}

// Load reads persisted rounds and speed. A process restart always starts stopped.
// Round IDs continue from the stored counter. The simulation loop is not resumed.
func (m *Manager) Load(ctx context.Context) error {
	data, err := m.repo.Load(ctx, m.historyLimit)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.speedMS = data.SpeedMS
	if m.speedMS < model.MinSpeedMS || m.speedMS > model.MaxSpeedMS {
		m.speedMS = model.DefaultSpeedMS
	}
	m.stats = data.Stats
	m.rounds = data.Rounds
	m.status = model.StatusStopped
	m.lastError = ""
	return nil
}

// Snapshot reads the database and overlays the live in-memory status.
// It does not consume a sequence number.
func (m *Manager) Snapshot(ctx context.Context) (Envelope, error) {
	data, err := m.repo.Load(ctx, m.historyLimit)
	if err != nil {
		return Envelope{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return Envelope{
		V:     model.ProtocolVersion,
		Type:  EventSnapshot,
		Seq:   m.seq,
		State: m.overlayLocked(data),
	}, nil
}

// Rounds returns the newest rounds first.
func (m *Manager) Rounds(ctx context.Context, limit int) ([]model.Round, error) {
	return m.repo.ListRounds(ctx, limit)
}

// Ping checks the repository.
func (m *Manager) Ping(ctx context.Context) error {
	return m.repo.Ping(ctx)
}

// Start begins the single simulation loop from the stopped state.
// Calling Start while running does not start another loop.
// Calling Start while paused returns ErrPaused.
func (m *Manager) Start(ctx context.Context) (Envelope, error) {
	m.life.Lock()
	defer m.life.Unlock()

	m.mu.Lock()
	status := m.status
	alive := m.loopAlive
	cancel := m.loopCancel
	m.mu.Unlock()

	switch status {
	case model.StatusRunning:
		return m.currentEnvelope(), nil
	case model.StatusPaused:
		return Envelope{}, ErrPaused
	}

	if alive {
		m.cancelLoop(cancel)
	}

	m.mu.Lock()
	if m.status == model.StatusRunning && m.loopAlive {
		env := m.envelopeLocked(EventSnapshot, false)
		m.mu.Unlock()
		return env, nil
	}
	m.status = model.StatusRunning
	m.lastError = ""
	m.loopAlive = true
	m.loopGen++
	gen := m.loopGen
	loopCtx, loopCancel := context.WithCancel(context.Background())
	m.loopCtx = loopCtx
	m.loopCancel = loopCancel
	m.wg.Add(1)
	env := m.envelopeLocked(EventStatus, true)
	pub := m.pub
	m.mu.Unlock()

	go m.loop(gen)
	m.publish(pub, env)
	return env, nil
}

// Pause stops new rounds. A round already being saved is allowed to finish.
func (m *Manager) Pause(context.Context) (Envelope, error) {
	m.life.Lock()
	defer m.life.Unlock()
	m.mu.Lock()
	if m.status == model.StatusStopped {
		m.mu.Unlock()
		return Envelope{}, ErrNotRunning
	}
	m.status = model.StatusPaused
	env := m.envelopeLocked(EventStatus, true)
	pub := m.pub
	m.mu.Unlock()
	m.poke()
	m.publish(pub, env)
	return env, nil
}

// Resume continues a paused loop. It does not create a second loop.
func (m *Manager) Resume(context.Context) (Envelope, error) {
	m.life.Lock()
	defer m.life.Unlock()
	m.mu.Lock()
	switch m.status {
	case model.StatusRunning:
		env := m.envelopeLocked(EventSnapshot, false)
		m.mu.Unlock()
		return env, nil
	case model.StatusStopped:
		m.mu.Unlock()
		return Envelope{}, ErrNotPaused
	}
	m.status = model.StatusRunning
	m.lastError = ""
	env := m.envelopeLocked(EventStatus, true)
	pub := m.pub
	m.mu.Unlock()
	m.poke()
	m.publish(pub, env)
	return env, nil
}

// Reset stops the loop, deletes simulated rounds, and restarts IDs at 1.
// The speed setting is kept. Tables are not dropped.
func (m *Manager) Reset(ctx context.Context) (Envelope, error) {
	m.life.Lock()
	defer m.life.Unlock()

	m.mu.Lock()
	m.status = model.StatusStopped
	m.loopGen++
	cancel := m.loopCancel
	m.mu.Unlock()
	m.cancelLoop(cancel)

	if err := m.repo.Reset(ctx); err != nil {
		return Envelope{}, err
	}

	m.mu.Lock()
	m.rounds = nil
	m.stats = model.Stats{}
	m.lastError = ""
	m.loopAlive = false
	m.loopCancel = nil
	m.loopCtx = nil
	env := m.envelopeLocked(EventSnapshot, true)
	pub := m.pub
	m.mu.Unlock()
	m.publish(pub, env)
	return env, nil
}

// SetSpeed stores the interval used before each subsequent round.
func (m *Manager) SetSpeed(ctx context.Context, speedMS int) (Envelope, error) {
	if speedMS < model.MinSpeedMS || speedMS > model.MaxSpeedMS {
		return Envelope{}, ErrInvalidSpeed
	}
	m.life.Lock()
	defer m.life.Unlock()
	if err := m.repo.SetSpeed(ctx, speedMS); err != nil {
		return Envelope{}, err
	}
	m.mu.Lock()
	m.speedMS = speedMS
	env := m.envelopeLocked(EventStatus, true)
	pub := m.pub
	m.mu.Unlock()
	m.publish(pub, env)
	return env, nil
}

// Stop ends the loop during process shutdown. It does not delete rounds.
func (m *Manager) Stop() {
	m.life.Lock()
	defer m.life.Unlock()
	m.mu.Lock()
	m.status = model.StatusStopped
	cancel := m.loopCancel
	m.mu.Unlock()
	m.cancelLoop(cancel)
	m.mu.Lock()
	m.loopAlive = false
	m.loopCancel = nil
	m.loopCtx = nil
	m.mu.Unlock()
}

func (m *Manager) loop(gen int) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		if m.loopGen == gen {
			m.loopAlive = false
			m.loopCancel = nil
			m.loopCtx = nil
		}
		m.mu.Unlock()
	}()
	m.loopStarts.Add(1)
	// A previous pause, resume, or reset can leave one wake in the buffer
	// after that loop has already exited. Drop it before this generation waits,
	// or the first interval is skipped and two rounds are played back to back.
	m.drainWake()

	for {
		if m.contextDone() {
			return
		}
		m.mu.Lock()
		status := m.status
		ctx := m.loopCtx
		m.mu.Unlock()
		if status != model.StatusRunning {
			if !m.waitWake() {
				return
			}
			continue
		}
		if ctx == nil {
			return
		}
		if err := m.play(ctx, gen); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			m.fail(err)
			return
		}
		if !m.waitInterval() {
			return
		}
	}
}

func (m *Manager) play(ctx context.Context, gen int) error {
	outcome, err := sim.DrawFrom(m.random)
	if err != nil {
		return err
	}
	opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	round, stats, err := m.repo.ApplyRound(opCtx, outcome, m.now().UTC())
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.loopGen != gen || m.status == model.StatusStopped {
		m.mu.Unlock()
		return nil
	}
	m.rememberLocked(round, stats)
	env := m.envelopeLocked(EventRound, true)
	pub := m.pub
	m.mu.Unlock()
	m.publish(pub, env)
	return nil
}

func (m *Manager) fail(err error) {
	m.logger.Error("simulation round failed", "err", redact.Error(err))
	m.mu.Lock()
	m.status = model.StatusStopped
	m.lastError = "The simulator stopped because saving a round failed."
	env := m.envelopeLocked(EventStatus, true)
	pub := m.pub
	m.mu.Unlock()
	m.publish(pub, env)
}

func (m *Manager) waitInterval() bool {
	m.mu.Lock()
	delay := time.Duration(m.speedMS) * time.Millisecond
	ctx := m.loopCtx
	m.mu.Unlock()
	if ctx == nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-m.wake:
		return ctx.Err() == nil
	case <-timer.C:
		return true
	}
}

func (m *Manager) waitWake() bool {
	m.mu.Lock()
	ctx := m.loopCtx
	m.mu.Unlock()
	if ctx == nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-m.wake:
		return ctx.Err() == nil
	}
}

func (m *Manager) contextDone() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loopCtx == nil || m.loopCtx.Err() != nil
}

func (m *Manager) cancelLoop(cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	m.poke()
	m.wg.Wait()
	m.drainWake()
}

func (m *Manager) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) drainWake() {
	select {
	case <-m.wake:
	default:
	}
}

func (m *Manager) publish(pub Publisher, env Envelope) {
	if pub != nil {
		pub.Publish(env)
	}
}

func (m *Manager) currentEnvelope() Envelope {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.envelopeLocked(EventSnapshot, false)
}

func (m *Manager) rememberLocked(round model.Round, stats model.Stats) {
	m.stats = stats
	m.rounds = append(m.rounds, round)
	if m.historyLimit > 0 && len(m.rounds) > m.historyLimit {
		m.rounds = append([]model.Round(nil), m.rounds[len(m.rounds)-m.historyLimit:]...)
	}
}

func (m *Manager) envelopeLocked(eventType string, advance bool) Envelope {
	if advance {
		m.seq++
	}
	return Envelope{
		V:     model.ProtocolVersion,
		Type:  eventType,
		Seq:   m.seq,
		State: m.viewLocked(),
	}
}

func (m *Manager) viewLocked() model.State {
	rounds := append([]model.Round(nil), m.rounds...)
	if rounds == nil {
		rounds = []model.Round{}
	}
	state := model.State{
		Status:       m.status,
		SpeedMS:      m.speedMS,
		Stats:        m.stats,
		Rounds:       rounds,
		HistoryLimit: m.historyLimit,
		ServerTime:   m.now().UTC(),
		LastError:    m.lastError,
		Version:      model.Version,
	}
	state.Prepare()
	return state
}

func (m *Manager) overlayLocked(data store.Data) model.State {
	rounds := data.Rounds
	if rounds == nil {
		rounds = []model.Round{}
	}
	state := model.State{
		Status:       m.status,
		SpeedMS:      m.speedMS,
		Stats:        data.Stats,
		Rounds:       rounds,
		HistoryLimit: m.historyLimit,
		ServerTime:   m.now().UTC(),
		LastError:    m.lastError,
		Version:      model.Version,
	}
	state.Prepare()
	return state
}
