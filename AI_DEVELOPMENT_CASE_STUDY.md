# AI development case study

This is a local portfolio prototype. It is not a commercial casino deployment.

## What was built

An agent implemented a Go service that draws simulated baccarat outcomes, stores them in PostgreSQL, and pushes versioned state to a vanilla dashboard over WebSocket. The work stayed inside this project workspace. `.cursor/hooks.json` was not edited. No database password was requested or written into Git.

## Decisions

- The draw uses `crypto/rand` and rejection sampling so the 10,000 slots are unbiased. The weights are documented as a display model, not as card-shoe rules.
- One manager goroutine owns start, pause, resume, and reset. A second start finds the loop already alive and returns.
- Round IDs come from a locked `simulator_state` row. Reset deletes only `rounds` and rewinds that counter.
- Events carry the full state plus a process-local `seq`. The browser applies the next sequence number and refetches after a gap. A reconnect snapshot replaces local state.
- Each WebSocket client has a queue of 16. A full queue closes that client instead of growing memory.
- The listen address must be loopback. Connection strings are redacted in logs. `.env.example` uses `CHANGE_ME`.

## Corrections

Two test failures were real. `waitTotal` was missing, so the HTTP test did not compile. Reset could leave a wake signal buffered; the next loop then skipped its speed wait and inserted a second round immediately. The loop now discards that signal, and a regression test injects the stale wake. The original round-ID assertion was kept.

## Verification

The release session ran `go vet ./...`, `go build ./...`, `go test ./...`, and `go test -race ./...`. All exited 0. `DATABASE_URL` was empty, so the destructive Postgres test was not repeated.

The user had already reported, from their own Terminal, a successful login as `baccarat_app`, the database `baccarat_simulator`, and `TestPostgresPersistenceRestartAndDuplicates` as PASS. Those results were not re-executed here.

The user also reported manual browser UAT as PASS: initial dashboard, live rounds, bead road, statistics, two-browser synchronization, pause from the other browser, resume, speed changes, reset, and persistence after stopping and starting the Go server again. The agent did not open a browser for that pass.

`.cursor/` stays untracked because its hook file points at a machine-local guard. This write-up does not claim a GitHub push.

## Human verification

The reported local UAT is complete. Before any future commit, confirm `git status` does not list `.env`. A repeat of the browser checklist is only needed after further code changes.
