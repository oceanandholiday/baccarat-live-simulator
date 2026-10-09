# Baccarat Live Simulator

Local display simulator for Player, Banker, and Tie rounds. It is a portfolio prototype: a Go service, PostgreSQL, a WebSocket feed, and a vanilla HTML dashboard. It does not take wagers, connect to a casino, or move money.

The outcome model is a simplified weighted draw. It is not a shoe of cards and it is not an official Baccarat probability implementation. See [ARCHITECTURE.md](ARCHITECTURE.md).

## Architecture

- `cmd/server` starts the process, runs migrations, and shuts down on Ctrl+C.
- `internal/sim` draws one outcome from `crypto/rand`.
- `internal/state` keeps a single simulation loop and versioned events.
- `internal/store` persists rounds in PostgreSQL, with an in-memory store for tests.
- `internal/ws` broadcasts events to browsers and drops slow clients.
- `internal/httpapi` serves JSON, `/ws`, and the files in `web/`.

```text
Browser --HTTP/WS--> API --> state manager --> simulator
                         \-> PostgreSQL
```

## Features

- Start, pause, resume, and reset. A second start does not create another loop.
- Speed from 200 ms to 30 s. The dashboard offers 0.5s, 1s, 2s, 5s, and 10s.
- Sequential round IDs that continue after a restart.
- All-time counts and one-decimal percentages.
- Bead road and recent history.
- WebSocket snapshots, live updates, and automatic reconnect.
- Reset deletes rows in the simulator's `rounds` table and sets the next ID to 1. It does not drop tables.

## Screenshots

No screenshots are stored in the repository. After the server is running, open `http://127.0.0.1:8080` and capture the dashboard yourself:

1. Start the simulator and wait for a few rounds.
2. Capture the desktop layout, including the bead road and connection pill.
3. Narrow the browser to a phone width and capture the stacked layout.
4. Pause, reconnect the browser, and confirm the same totals are still shown.

## Setup

PostgreSQL 17 is expected on `127.0.0.1:5432` with database `baccarat_simulator` and role `baccarat_app`. The password stays on your machine.

```bash
cp .env.example .env
```

Edit `.env` and replace `CHANGE_ME`. Do not commit `.env`.

`go.sum` is produced by the module tool. From the repository root:

```bash
go get github.com/jackc/pgx/v5@latest github.com/coder/websocket@latest
go mod tidy
```

## Run

The server refuses any listen address that is not loopback. The default is `127.0.0.1:8080`.

```bash
set -a
. ./.env
set +a
go run ./cmd/server
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080).

A restart loads saved rounds and the saved speed, then leaves the simulator stopped. It does not resume a loop that was running when the process exited.

## Test

```bash
gofmt -w $(find . -name '*.go' -not -path './.git/*')
go vet ./...
go test ./...
go test -race ./...
go build -o bin/server ./cmd/server
```

`TestPostgresPersistenceRestartAndDuplicates` runs only when `DATABASE_URL` is set. It checks that the database name is `baccarat_simulator` and the role is `baccarat_app`, then deletes rows from the simulator `rounds` table. Do not point it at another database.

`go vet ./...` and `go build ./...` passed in this workspace. `go test ./...` and `go test -race ./...` also passed here. `DATABASE_URL` was unset for those runs, so the Postgres test did not run. The user separately ran that integration test against `baccarat_simulator` as `baccarat_app` and reported PASS. Details are in [TEST_REPORT.md](TEST_REPORT.md).

## Manual UAT

The user ran the local server and reported PASS for the dashboard empty state, live rounds, bead road, Player / Banker / Tie statistics, two-browser updates, pause from another browser, resume, speed changes, reset, and round history after a server restart. The agent did not repeat that browser session. This remains a local prototype, not a production deployment.

To run it again, from this directory, without printing `DATABASE_URL`:

```bash
set -a
. ./.env
set +a
go run ./cmd/server
```

Open http://127.0.0.1:8080. Stop the server with Ctrl+C.

## Security

- Bind only to loopback.
- Keep `DATABASE_URL` in `.env`, which is gitignored.
- `.env.example` contains the placeholder `CHANGE_ME` only.
- Reset is limited to `rounds` and `simulator_state`.
- This is not a production gambling system.

## Project docs

- [ARCHITECTURE.md](ARCHITECTURE.md)
- [API.md](API.md)
- [TASK_STATE.md](TASK_STATE.md)
- [TEST_REPORT.md](TEST_REPORT.md)
- [AI_DEVELOPMENT_CASE_STUDY.md](AI_DEVELOPMENT_CASE_STUDY.md)
