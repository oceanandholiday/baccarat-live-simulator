# API

Base URL: `http://127.0.0.1:8080`

The server rejects a non-loopback `ADDR`. Browsers should use the same origin. WebSocket origins must match `127.0.0.1:*` or `localhost:*`.

Errors use `{ "error": "..." }`. Unknown JSON fields are rejected. Request bodies are limited to 4096 bytes.

## `GET /api/health`

`200`

```json
{"status":"ok","database":"ok","version":"0.1.0"}
```

`503` when the database ping fails. `database` is then `unavailable`. The response never includes the connection string.

## `GET /api/state`

Returns the versioned envelope. `seq` is not incremented.

```json
{
  "v": 1,
  "type": "snapshot",
  "seq": 4,
  "state": {
    "status": "paused",
    "speedMs": 2000,
    "stats": {"total": 3, "counts": {"player": 1, "banker": 1, "tie": 1}},
    "percentages": {"player": 33.3, "banker": 33.3, "tie": 33.3},
    "rounds": [
      {"id": 1, "outcome": "player", "createdAt": "2026-10-09T04:00:00Z"}
    ],
    "historyLimit": 120,
    "serverTime": "2026-10-09T04:00:03Z",
    "version": "0.1.0"
  }
}
```

`status` is `stopped`, `running`, or `paused`. `rounds` is oldest first. An empty history is `[]`, not null.

## `GET /api/rounds?limit=50`

`limit` defaults to 50. Values outside 1–500 return `400`.

Rounds are newest first.

```json
{"rounds":[{"id":3,"outcome":"tie","createdAt":"2026-10-09T04:00:02Z"}]}
```

## `POST /api/simulation/start`

Body is optional.

```json
{"speedMs": 2000}
```

From `stopped`, this starts the only loop and returns `200` with a `status` envelope. Calling it again while running returns `200` and does not start a second loop.

`409` while paused: `simulation is paused; use resume`.

If `speedMs` is present, it is saved before the start attempt. An out-of-range speed returns `400` and does not start the loop.

## `POST /api/simulation/pause`

`200` when the status becomes `paused`. Also `200` if it was already paused.

`409` when stopped.

A round already inside the database transaction can still commit. The next draw does not start.

## `POST /api/simulation/resume`

`200` from `paused`. `200` if it is already running. `409` when stopped.

## `POST /api/simulation/reset`

Stops the loop, deletes every row in `rounds`, and sets `next_round_id` back to 1. Speed is unchanged. Response type is `snapshot`.

This is destructive for simulated rounds in this database. The dashboard asks for a second click before sending it.

## `POST /api/simulation/speed`

```json
{"speedMs": 1000}
```

`speedMs` is required and must be from 200 to 30000 inclusive. The new interval is used on the next wait. The current wait is not interrupted.

## `GET /ws`

WebSocket upgrade. The first message is a `snapshot` envelope. Later messages use the same schema as `GET /api/state`.

A client ignores `round` and `status` events unless `seq` is exactly one greater than the last applied sequence. On a gap it calls `GET /api/state`. After the socket opens, the page treats the next snapshot as authoritative, including when a restarted process has a lower `seq` than the previous process.

There is no client-to-server command on this socket. Use the HTTP controls.

## Status codes

| Code | Meaning |
| --- | --- |
| 200 | Success |
| 400 | Invalid JSON, unknown field, or bad speed or limit |
| 404 | Unknown path |
| 405 | Method not allowed |
| 409 | Illegal control transition |
| 500 | Unexpected server or database failure, without a connection string |
| 503 | Health check could not ping the database |
