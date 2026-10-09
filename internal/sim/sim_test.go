package sim

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"baccarat-live-simulator/internal/model"
)

func TestWeightsFillTheScale(t *testing.T) {
	if WeightPlayer+WeightBanker+WeightTie != WeightScale {
		t.Fatalf("weights sum to %d, want %d", WeightPlayer+WeightBanker+WeightTie, WeightScale)
	}
}

func TestOutcomeBoundaries(t *testing.T) {
	cases := []struct {
		n    int
		want model.Outcome
	}{
		{0, model.Player},
		{WeightPlayer - 1, model.Player},
		{WeightPlayer, model.Banker},
		{WeightPlayer + WeightBanker - 1, model.Banker},
		{WeightPlayer + WeightBanker, model.Tie},
		{WeightScale - 1, model.Tie},
	}
	for _, tc := range cases {
		got, err := OutcomeFromIndex(tc.n)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("index %d = %s, want %s", tc.n, got, tc.want)
		}
	}
	if _, err := OutcomeFromIndex(-1); err == nil {
		t.Fatal("expected range error")
	}
	if _, err := OutcomeFromIndex(WeightScale); err == nil {
		t.Fatal("expected range error")
	}
}

func TestIndexFromUint64RejectsBias(t *testing.T) {
	limit := (^uint64(0) / WeightScale) * WeightScale
	if _, ok := IndexFromUint64(limit); ok {
		t.Fatal("limit value should be rejected")
	}
	idx, ok := IndexFromUint64(0)
	if !ok || idx != 0 {
		t.Fatalf("0 -> %d ok=%v", idx, ok)
	}
	idx, ok = IndexFromUint64(WeightScale + 7)
	if !ok || idx != 7 {
		t.Fatalf("scaled value -> %d ok=%v", idx, ok)
	}
}

func TestDrawFromKnownBytes(t *testing.T) {
	if _, err := DrawFrom(nil); err == nil {
		t.Fatal("nil reader should fail")
	}
	got, err := DrawFrom(bytes.NewReader(uint64Bytes(0)))
	if err != nil {
		t.Fatal(err)
	}
	if got != model.Player {
		t.Fatalf("got %s", got)
	}

	bankerSlot := uint64(WeightPlayer)
	got, err = DrawFrom(bytes.NewReader(uint64Bytes(bankerSlot)))
	if err != nil {
		t.Fatal(err)
	}
	if got != model.Banker {
		t.Fatalf("got %s", got)
	}
}

func TestDrawFromSkipsRejectedSample(t *testing.T) {
	limit := (^uint64(0) / WeightScale) * WeightScale
	var raw []byte
	raw = append(raw, uint64Bytes(limit)...)
	raw = append(raw, uint64Bytes(uint64(WeightPlayer+WeightBanker))...)
	got, err := DrawFrom(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got != model.Tie {
		t.Fatalf("got %s, want tie", got)
	}
}

func TestDrawCryptoSource(t *testing.T) {
	for i := 0; i < 64; i++ {
		got, err := Draw()
		if err != nil {
			t.Fatal(err)
		}
		if !got.Valid() {
			t.Fatalf("invalid outcome %q", got)
		}
	}
}

func TestDrawFromShortReader(t *testing.T) {
	_, err := DrawFrom(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("expected short read")
	}
}

type endless func([]byte) (int, error)

func (e endless) Read(p []byte) (int, error) { return e(p) }

func TestDrawFromAlwaysRejected(t *testing.T) {
	limit := (^uint64(0) / WeightScale) * WeightScale
	raw := uint64Bytes(limit)
	r := endless(func(p []byte) (int, error) {
		copy(p, bytes.Repeat(raw, len(p)/8+1))
		return len(p), nil
	})
	if _, err := DrawFrom(r); err == nil {
		t.Fatal("expected rejection failure")
	}
}

func TestDrawFromEmpty(t *testing.T) {
	_, err := DrawFrom(bytes.NewReader(nil))
	if err == nil {
		t.Fatal(io.EOF)
	}
}

func uint64Bytes(v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return buf[:]
}

func FuzzOutcomeFromIndex(f *testing.F) {
	f.Add(0)
	f.Add(WeightPlayer)
	f.Add(WeightScale - 1)
	f.Fuzz(func(t *testing.T, n int) {
		got, err := OutcomeFromIndex(n)
		if n < 0 || n >= WeightScale {
			if err == nil {
				t.Fatal("expected error")
			}
			return
		}
		if err != nil || !got.Valid() {
			t.Fatalf("index %d -> %s err=%v", n, got, err)
		}
	})
}
