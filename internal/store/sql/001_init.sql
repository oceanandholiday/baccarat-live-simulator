-- Simulator-owned tables. Reset deletes rows in rounds and rewinds
-- simulator_state.next_round_id. It does not drop these tables.

CREATE TABLE IF NOT EXISTS simulator_state (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    speed_ms INTEGER NOT NULL CHECK (speed_ms BETWEEN 200 AND 30000),
    next_round_id BIGINT NOT NULL CHECK (next_round_id >= 1)
);

INSERT INTO simulator_state (id, speed_ms, next_round_id)
VALUES (1, 2000, 1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS rounds (
    id BIGINT PRIMARY KEY CHECK (id >= 1),
    outcome TEXT NOT NULL CHECK (outcome IN ('player', 'banker', 'tie')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
