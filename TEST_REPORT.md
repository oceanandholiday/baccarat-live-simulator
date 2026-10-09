# Test report

This is a working local prototype, not a production deployment. No screenshots or performance figures are included.

## Who ran what

| Evidence | Who | Result |
| --- | --- | --- |
| `go vet ./...` | Agent, this release session | Exit 0 |
| `go build ./...` | Agent, this release session | Exit 0 |
| `go test ./...` | Agent, this release session | Exit 0. `DATABASE_URL` was unset, so the Postgres test did not run |
| `go test -race ./...` | Agent, this release session | Exit 0. No race reported. Postgres test did not run |
| Login as `baccarat_app` to `baccarat_simulator` | User, earlier Terminal session | User reported PASS |
| `TestPostgresPersistenceRestartAndDuplicates` | User, with `DATABASE_URL` set | User reported PASS. Not rerun here |
| Manual browser UAT | User, on the running local server | User reported PASS for the items below |

The agent did not repeat the destructive Postgres test. That test deletes every row in `rounds` and rewinds the next round ID.

### Agent: `go test ./...`

```text
?   	baccarat-live-simulator/cmd/server	[no test files]
ok  	baccarat-live-simulator/internal/config	(cached)
ok  	baccarat-live-simulator/internal/httpapi	0.521s
ok  	baccarat-live-simulator/internal/model	(cached)
ok  	baccarat-live-simulator/internal/redact	(cached)
ok  	baccarat-live-simulator/internal/sim	(cached)
ok  	baccarat-live-simulator/internal/state	(cached)
ok  	baccarat-live-simulator/internal/store	(cached)
?   	baccarat-live-simulator/internal/ws	[no test files]
?   	baccarat-live-simulator/web	[no test files]
```

### Agent: `go test -race ./...`

```text
?   	baccarat-live-simulator/cmd/server	[no test files]
ok  	baccarat-live-simulator/internal/config	(cached)
ok  	baccarat-live-simulator/internal/httpapi	1.479s
ok  	baccarat-live-simulator/internal/model	(cached)
ok  	baccarat-live-simulator/internal/redact	(cached)
ok  	baccarat-live-simulator/internal/sim	(cached)
ok  	baccarat-live-simulator/internal/state	(cached)
ok  	baccarat-live-simulator/internal/store	(cached)
?   	baccarat-live-simulator/internal/ws	[no test files]
?   	baccarat-live-simulator/web	[no test files]
```

In-memory HTTP tests covered validation, start, pause, resume, reset, WebSocket broadcast, and reconnect. They are not a substitute for the user's browser session.

## User-reported browser UAT

Reported PASS, not re-executed by the agent:

- Dashboard initial state
- Live simulation and round generation
- Bead road rendering
- Player, Banker, and Tie statistics
- Two-browser real-time synchronization
- Pause from the other browser
- Resume
- Speed changes
- Reset
- PostgreSQL persistence after stopping and restarting the Go server

## Fixes already in the tree

- `waitTotal` waits for the first round in the HTTP control test.
- A leftover wake after reset could skip the speed interval and insert a second round immediately. The next loop discards that wake. `TestStaleWakeAfterResetDoesNotSkipTheInterval` covers it.

## Limitations

- Percentages can sum to 99.9 or 100.1 because each value is rounded separately.
- The bead road and history show at most the latest 120 rounds. Totals include every stored round.
- Event `seq` restarts with the process. Round IDs do not.
- A process restart loads history and leaves the simulator stopped.
- The agent did not open a browser during this release session.
