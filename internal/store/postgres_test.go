package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"baccarat-live-simulator/internal/model"
)

func TestPostgresPersistenceRestartAndDuplicates(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	db, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = db.Reset(cleanupCtx)
		db.Close()
	})

	var dbName, dbUser string
	if err := db.pool.QueryRow(ctx, `SELECT current_database(), current_user`).Scan(&dbName, &dbUser); err != nil {
		t.Fatal(err)
	}
	if dbName != "baccarat_simulator" {
		t.Fatalf("connected to database %s, want baccarat_simulator", dbName)
	}
	if dbUser != "baccarat_app" {
		t.Fatalf("connected as %s, want baccarat_app", dbUser)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := db.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	first, stats, err := db.ApplyRound(ctx, model.Player, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 1 || stats.Total != 1 || stats.Counts.Player != 1 {
		t.Fatalf("first = %+v stats=%+v", first, stats)
	}
	second, stats, err := db.ApplyRound(ctx, model.Banker, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != 2 || stats.Counts.Banker != 1 || stats.Total != 2 {
		t.Fatalf("second = %+v stats=%+v", second, stats)
	}

	_, err = db.pool.Exec(ctx, `INSERT INTO rounds (id, outcome, created_at) VALUES ($1, 'tie', now())`, second.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate insert err = %v", err)
	}
	if _, _, err := db.ApplyRound(ctx, model.Outcome("void"), now); err == nil {
		t.Fatal("expected invalid outcome")
	}

	if err := db.SetSpeed(ctx, 1500); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(reopened.Close)
	data, err := reopened.Load(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.NextRoundID != 3 || data.SpeedMS != 1500 || data.Stats.Total != 2 || len(data.Rounds) != 2 {
		t.Fatalf("reloaded = %+v", data)
	}
	if data.Rounds[0].ID != 1 || data.Rounds[1].Outcome != model.Banker {
		t.Fatalf("rounds = %+v", data.Rounds)
	}

	const workers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if _, _, err := reopened.ApplyRound(ctx, model.Tie, time.Now().UTC()); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	data, err = reopened.Load(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if data.Stats.Total != int64(2+workers) || data.NextRoundID != int64(3+workers) {
		t.Fatalf("after concurrent insert = %+v", data)
	}
	if err := reopened.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	data, err = reopened.Load(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if data.Stats.Total != 0 || data.NextRoundID != 1 || len(data.Rounds) != 0 || data.SpeedMS != 1500 {
		t.Fatalf("after reset = %+v", data)
	}
}
