package config

import "testing"

func TestLoadDefaultsAndRejectsPublicBind(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing DATABASE_URL")
	}

	t.Setenv("DATABASE_URL", "postgres://baccarat_app:example@127.0.0.1:5432/baccarat_simulator?sslmode=disable")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("addr = %s", cfg.Addr)
	}

	t.Setenv("ADDR", "0.0.0.0:8080")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-loopback rejection")
	}

	t.Setenv("ADDR", "localhost:9090")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "localhost:9090" {
		t.Fatalf("addr = %s", cfg.Addr)
	}
}

func TestValidateAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:1", "[::1]:8080", "localhost:80"} {
		if err := validateAddr(addr); err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{"", "8080", "example.com:8080", "10.0.0.8:8080", "127.0.0.1:0", "127.0.0.1:99999"} {
		if err := validateAddr(addr); err == nil {
			t.Fatalf("%s should fail", addr)
		}
	}
}
