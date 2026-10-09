package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed sql/*.sql
var migrationFS embed.FS

const bootstrapMigrations = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies embedded SQL files in name order.
// Each file runs in one transaction and is recorded in schema_migrations.
// Statements are idempotent so a crashed apply can be repeated safely.
func (p *Postgres) Migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, bootstrapMigrations); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrationFS, "sql")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		var applied bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied {
			continue
		}
		body, err := migrationFS.ReadFile("sql/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := p.applyMigration(ctx, version, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) applyMigration(ctx context.Context, version int, body string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range splitSQL(body) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("migration %s is missing a numeric prefix", name)
	}
	version, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, fmt.Errorf("migration %s: %w", name, err)
	}
	return version, nil
}

func splitSQL(body string) []string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	parts := strings.Split(b.String(), ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
