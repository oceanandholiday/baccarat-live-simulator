package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"baccarat-live-simulator/internal/model"
	"baccarat-live-simulator/internal/redact"
)

const (
	maxConns         = int32(8)
	historyCap       = 500
	dbOpTimeout      = 3 * time.Second
	connectTimeout   = 5 * time.Second
	statementTimeout = "15000"
)

// Postgres stores rounds in the baccarat_simulator database.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens a limited pool and checks that the connection works.
// databaseURL is not written to logs by this function.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %s", redact.Error(err))
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "baccarat-live-simulator"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %s", redact.Error(err))
	}
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %s", redact.Error(err))
	}
	return &Postgres{pool: pool}, nil
}

// Close releases the pool.
func (p *Postgres) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}

// Pool exposes the pool for integration tests in this module.
func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

func (p *Postgres) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()
	return p.pool.Ping(ctx)
}

func (p *Postgres) Load(ctx context.Context, historyLimit int) (Data, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()
	historyLimit = capHistory(historyLimit)

	var data Data
	err := p.pool.QueryRow(ctx, `SELECT speed_ms, next_round_id FROM simulator_state WHERE id = 1`).Scan(&data.SpeedMS, &data.NextRoundID)
	if err != nil {
		return Data{}, fmt.Errorf("load simulator state: %w", err)
	}
	stats, err := p.stats(ctx, p.pool)
	if err != nil {
		return Data{}, err
	}
	rounds, err := p.recent(ctx, p.pool, historyLimit)
	if err != nil {
		return Data{}, err
	}
	data.Stats = stats
	data.Rounds = rounds
	return data, nil
}

func (p *Postgres) ApplyRound(ctx context.Context, outcome model.Outcome, at time.Time) (model.Round, model.Stats, error) {
	if !outcome.Valid() {
		return model.Round{}, model.Stats{}, ErrInvalidOutcome
	}
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.Round{}, model.Stats{}, fmt.Errorf("begin round: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	if err := tx.QueryRow(ctx, `SELECT next_round_id FROM simulator_state WHERE id = 1 FOR UPDATE`).Scan(&id); err != nil {
		return model.Round{}, model.Stats{}, fmt.Errorf("lock next round id: %w", err)
	}
	round := model.Round{ID: id, Outcome: outcome, CreatedAt: at.UTC()}
	if _, err := tx.Exec(ctx, `INSERT INTO rounds (id, outcome, created_at) VALUES ($1, $2, $3)`, round.ID, string(round.Outcome), round.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return model.Round{}, model.Stats{}, ErrDuplicateRound
		}
		return model.Round{}, model.Stats{}, fmt.Errorf("insert round: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE simulator_state SET next_round_id = $1 WHERE id = 1`, id+1); err != nil {
		return model.Round{}, model.Stats{}, fmt.Errorf("advance round id: %w", err)
	}
	stats, err := p.stats(ctx, tx)
	if err != nil {
		return model.Round{}, model.Stats{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Round{}, model.Stats{}, fmt.Errorf("commit round: %w", err)
	}
	return round, stats, nil
}

func (p *Postgres) ListRounds(ctx context.Context, limit int) ([]model.Round, error) {
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()
	if limit <= 0 {
		return []model.Round{}, nil
	}
	if limit > model.MaxRoundQuery {
		limit = model.MaxRoundQuery
	}
	rows, err := p.pool.Query(ctx, `SELECT id, outcome, created_at FROM rounds ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list rounds: %w", err)
	}
	defer rows.Close()
	out := make([]model.Round, 0, limit)
	for rows.Next() {
		var round model.Round
		var outcome string
		if err := rows.Scan(&round.ID, &outcome, &round.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan round: %w", err)
		}
		round.Outcome = model.Outcome(outcome)
		out = append(out, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rounds: %w", err)
	}
	return out, nil
}

func (p *Postgres) SetSpeed(ctx context.Context, speedMS int) error {
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()
	tag, err := p.pool.Exec(ctx, `UPDATE simulator_state SET speed_ms = $1 WHERE id = 1`, speedMS)
	if err != nil {
		return fmt.Errorf("set speed: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("simulator state row is missing")
	}
	return nil
}

// Reset removes every simulated round and sets the next ID back to 1.
// Speed is preserved. This is limited to the rounds and simulator_state tables.
func (p *Postgres) Reset(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, dbOpTimeout)
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM rounds`); err != nil {
		return fmt.Errorf("delete rounds: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE simulator_state SET next_round_id = 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("rewind round id: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (p *Postgres) stats(ctx context.Context, q queryer) (model.Stats, error) {
	var stats model.Stats
	err := q.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE outcome = 'player'),
			COUNT(*) FILTER (WHERE outcome = 'banker'),
			COUNT(*) FILTER (WHERE outcome = 'tie')
		FROM rounds`).Scan(&stats.Counts.Player, &stats.Counts.Banker, &stats.Counts.Tie)
	if err != nil {
		return model.Stats{}, fmt.Errorf("count rounds: %w", err)
	}
	stats.Normalize()
	return stats, nil
}

func (p *Postgres) recent(ctx context.Context, q queryer, limit int) ([]model.Round, error) {
	rows, err := q.Query(ctx, `SELECT id, outcome, created_at FROM rounds ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("load recent rounds: %w", err)
	}
	defer rows.Close()
	desc := make([]model.Round, 0, limit)
	for rows.Next() {
		var round model.Round
		var outcome string
		if err := rows.Scan(&round.ID, &outcome, &round.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan recent round: %w", err)
		}
		round.Outcome = model.Outcome(outcome)
		desc = append(desc, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load recent rounds: %w", err)
	}
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	if desc == nil {
		desc = []model.Round{}
	}
	return desc, nil
}

func capHistory(n int) int {
	if n <= 0 || n > historyCap {
		if n <= 0 {
			return model.DefaultHistory
		}
		return historyCap
	}
	return n
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
