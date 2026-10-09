# Architecture

## Components

```text
web/index.html + app.js
        |  REST and WebSocket
        v
internal/httpapi --------> internal/ws hub
        |
        v
internal/state manager ---> internal/sim (crypto/rand)
        |
        v
internal/store postgres ---> baccarat_simulator
```

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Configuration, migration, signals, shutdown |
| `internal/config` | `ADDR` and `DATABASE_URL`; loopback check |
| `internal/sim` | Weighted outcome draw |
| `internal/state` | One loop, controls, event sequence |
| `internal/store` | SQL migrations and round persistence |
| `internal/ws` | Bounded per-client queues |
| `internal/httpapi` | JSON, `/ws`, embedded dashboard |
| `web` | HTML, CSS, and JavaScript |

HTTP write and read timeouts are not set on the shared listener, because a WebSocket stays open. JSON handlers use a 5 second request timeout. WebSocket writes use a 5 second timeout of their own.

## Simulation model

Each round takes 8 bytes from `crypto/rand`. Values in the biased tail of the `uint64` range are rejected. The remaining value maps onto 10,000 equal slots:

| Slots | Outcome |
| --- | --- |
| 0–4461 | player |
| 4462–9047 | banker |
| 9048–9999 | tie |

These weights are fixed display parameters. They are only in the rough neighborhood of commonly quoted eight-deck baccarat shares. The program does not shuffle cards, apply tableau drawing rules, commission, or side bets.

The running loop sleeps for the configured speed after each successful round. Pause finishes a round that is already being saved, then waits. Resume wakes that same goroutine. Start while running returns the current state and does not launch a second goroutine. Start while paused returns HTTP 409.

On process start, stored rounds and speed are loaded and the status is forced to `stopped`.

## Data flow

1. The loop draws an outcome.
2. `ApplyRound` locks `simulator_state`, inserts that `next_round_id`, and increments the counter in one transaction.
3. The manager appends the round to a 120-round memory window and increments `seq`.
4. The hub JSON-encodes the full state and queues it for each client.
5. A browser applies the event only when `seq` is the next expected value. A gap triggers `GET /api/state`.
6. A new socket always receives a snapshot first. `seq` is the process sequence, not a durable log offset. Reconnect treats the snapshot as authoritative.

Slow clients have a queue of 16 messages. A full queue disconnects that client. The hub allows 100 clients. Shutdown cancels the hub context and closes the queues.

## Database

Migration `internal/store/sql/001_init.sql` creates:

- `schema_migrations`
- `simulator_state` (`id = 1`, `speed_ms`, `next_round_id`)
- `rounds` (`id`, `outcome`, `created_at`) with a primary key on `id`

`outcome` is constrained to `player`, `banker`, or `tie`. IDs are allocated only inside `ApplyRound`, so two callers cannot take the same ID. A direct insert of an existing ID fails with unique violation `23505`.

Reset, in one transaction:

```sql
DELETE FROM rounds;
UPDATE simulator_state SET next_round_id = 1 WHERE id = 1;
```

Speed is kept. Tables are not dropped. No other schema is touched.

The pool uses at most 8 connections, a 15 second statement timeout, and the application name `baccarat-live-simulator`. Connection errors are redacted before logging.

## WebSocket protocol

Protocol version is `1`. Every state message has this shape:

```json
{
  "v": 1,
  "type": "snapshot",
  "seq": 0,
  "state": {}
}
```

| `type` | When |
| --- | --- |
| `snapshot` | Socket connect, reset, and idempotent reads |
| `round` | One committed round |
| `status` | Start, pause, resume, stop, speed |

`state.rounds` is chronological, oldest first, capped at 120. `state.stats` counts every stored round. Percentages are rounded independently to one decimal and may not add up to 100.

Client-to-server WebSocket payloads are ignored. Ping frames go out every 25 seconds.

## Shutdown

Ctrl+C stops the loop, closes WebSocket writers, then shuts down HTTP. Rounds are not deleted.
