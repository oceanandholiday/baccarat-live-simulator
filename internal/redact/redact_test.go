package redact

import (
	"errors"
	"testing"
)

func TestErrorRedactsURL(t *testing.T) {
	err := errors.New("connect postgres://baccarat_app:secret@127.0.0.1:5432/baccarat_simulator failed")
	got := Error(err)
	if stringsContains(got, "secret") || stringsContains(got, "baccarat_app:") {
		t.Fatalf("redaction failed: %s", got)
	}
	if !stringsContains(got, "postgres://REDACTED") {
		t.Fatalf("got %s", got)
	}
	if Error(nil) != "" {
		t.Fatal("nil error")
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && contains(s, sub)))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
