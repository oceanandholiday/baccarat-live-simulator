package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"baccarat-live-simulator/internal/model"
	"baccarat-live-simulator/internal/state"
	"baccarat-live-simulator/internal/store"
	"baccarat-live-simulator/internal/ws"
)

func TestAPIValidationAndControls(t *testing.T) {
	srv, _ := newTestServer(t)

	health := getJSON(t, srv.URL+"/api/health")
	if health["status"] != "ok" || health["database"] != "ok" {
		t.Fatalf("health = %+v", health)
	}

	res := postRaw(t, srv.URL+"/api/simulation/pause", `{}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("pause status = %d", res.StatusCode)
	}

	res = postRaw(t, srv.URL+"/api/simulation/speed", `{"speedMs": 10}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("speed status = %d", res.StatusCode)
	}

	res = postRaw(t, srv.URL+"/api/simulation/start", `{"speedMs": 50, "extra": true}`)
	if res.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("start status = %d body=%s", res.StatusCode, body)
	}
	res.Body.Close()

	res = get(t, srv.URL+"/api/rounds?limit=0")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = get(t, srv.URL+"/api/rounds?limit=nope")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit status = %d", res.StatusCode)
	}
	res.Body.Close()

	res = postRaw(t, srv.URL+"/api/health", `{}`)
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", res.StatusCode)
	}
	res.Body.Close()

	page := get(t, srv.URL+"/")
	if page.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", page.StatusCode)
	}
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(html), "Baccarat Live") {
		t.Fatal("dashboard HTML missing title")
	}

	env := postJSON(t, srv.URL+"/api/simulation/start", map[string]int{"speedMs": 10000})
	if env.State.Status != model.StatusRunning {
		t.Fatalf("start state = %+v", env.State)
	}
	waitTotal(t, srv.URL, 1)

	again := postJSON(t, srv.URL+"/api/simulation/start", map[string]int{})
	if again.State.Status != model.StatusRunning {
		t.Fatalf("second start = %+v", again.State)
	}

	paused := postJSON(t, srv.URL+"/api/simulation/pause", map[string]int{})
	if paused.State.Status != model.StatusPaused {
		t.Fatalf("pause = %+v", paused.State)
	}
	res = postRaw(t, srv.URL+"/api/simulation/start", `{}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("start while paused = %d", res.StatusCode)
	}
	res.Body.Close()

	resumed := postJSON(t, srv.URL+"/api/simulation/resume", map[string]int{})
	if resumed.State.Status != model.StatusRunning {
		t.Fatalf("resume = %+v", resumed.State)
	}
	postJSON(t, srv.URL+"/api/simulation/pause", map[string]int{})

	roundsRes := get(t, srv.URL+"/api/rounds?limit=5")
	defer roundsRes.Body.Close()
	var listed struct {
		Rounds []model.Round `json:"rounds"`
	}
	if err := json.NewDecoder(roundsRes.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Rounds) == 0 || listed.Rounds[0].ID < 1 {
		t.Fatalf("rounds = %+v", listed.Rounds)
	}

	reset := postJSON(t, srv.URL+"/api/simulation/reset", map[string]int{})
	if reset.State.Stats.Total != 0 || reset.State.Status != model.StatusStopped || reset.Type != state.EventSnapshot {
		t.Fatalf("reset = %+v", reset)
	}
}

func TestWebSocketBroadcastAndReconnect(t *testing.T) {
	srv, _ := newTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	first := dial(t, ctx, srv.URL)
	defer first.Close(websocket.StatusNormalClosure, "")
	second := dial(t, ctx, srv.URL)
	defer second.Close(websocket.StatusNormalClosure, "")

	a := readEnv(t, ctx, first)
	b := readEnv(t, ctx, second)
	if a.Type != state.EventSnapshot || b.Type != state.EventSnapshot {
		t.Fatalf("initial types %s %s", a.Type, b.Type)
	}

	postJSON(t, srv.URL+"/api/simulation/start", map[string]int{"speedMs": 10000})
	roundA := readUntilRound(t, ctx, first)
	roundB := readUntilRound(t, ctx, second)
	if roundA.State.Stats.Total < 1 || roundB.State.Stats.Total < 1 {
		t.Fatalf("broadcast totals %d %d", roundA.State.Stats.Total, roundB.State.Stats.Total)
	}
	if roundA.Seq < a.Seq || roundB.Seq < b.Seq {
		t.Fatalf("sequence did not advance: %d -> %d, %d -> %d", a.Seq, roundA.Seq, b.Seq, roundB.Seq)
	}

	first.Close(websocket.StatusNormalClosure, "")
	postJSON(t, srv.URL+"/api/simulation/pause", map[string]int{})
	reconnected := dial(t, ctx, srv.URL)
	defer reconnected.Close(websocket.StatusNormalClosure, "")
	snap := readEnv(t, ctx, reconnected)
	httpSnap := getState(t, srv.URL)
	if snap.Type != state.EventSnapshot {
		t.Fatalf("reconnect type %s", snap.Type)
	}
	if snap.State.Stats.Total != httpSnap.State.Stats.Total || snap.State.Status != httpSnap.State.Status {
		t.Fatalf("reconnect %+v http %+v", snap.State, httpSnap.State)
	}
	if snap.State.Stats.Total < 1 || snap.State.Status != model.StatusPaused {
		t.Fatalf("recovered state = %+v", snap.State)
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *state.Manager) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := state.New(store.NewMemory(), logger)
	hub := ws.New(logger)
	mgr.SetPublisher(hub)
	api := New("127.0.0.1:0", mgr, hub, logger)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(func() {
		mgr.Stop()
		hub.Close()
		srv.Close()
	})
	return srv, mgr
}

func dial(t *testing.T, ctx context.Context, httpURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(httpURL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readEnv(t *testing.T, ctx context.Context, conn *websocket.Conn) state.Envelope {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env state.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.V != model.ProtocolVersion {
		t.Fatalf("protocol = %d", env.V)
	}
	return env
}

func readUntilRound(t *testing.T, ctx context.Context, conn *websocket.Conn) state.Envelope {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		env := readEnv(t, deadline, conn)
		if env.Type == state.EventRound || env.State.Stats.Total > 0 && env.Type != state.EventSnapshot {
			return env
		}
		if env.State.Stats.Total > 0 {
			return env
		}
	}
}

func waitTotal(t *testing.T, base string, total int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last int64
	for time.Now().Before(deadline) {
		last = getState(t, base).State.Stats.Total
		if last >= total {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d rounds, have %d", total, last)
}

func getState(t *testing.T, base string) state.Envelope {
	t.Helper()
	res := get(t, base+"/api/state")
	defer res.Body.Close()
	var env state.Envelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	return env
}

func getJSON(t *testing.T, url string) map[string]string {
	t.Helper()
	res := get(t, url)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s status %d", url, res.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func postJSON(t *testing.T, url string, body any) state.Envelope {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res := postRaw(t, url, string(raw))
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%s status %d body %s", url, res.StatusCode, b)
	}
	var env state.Envelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	return env
}

func postRaw(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
