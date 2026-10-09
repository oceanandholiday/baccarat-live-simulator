// Package config loads runtime settings from the environment.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAddr = "127.0.0.1:8080"
	envAddr     = "ADDR"
	envDatabase = "DATABASE_URL"
)

// Config is the process configuration. DatabaseURL is never logged.
type Config struct {
	Addr        string
	DatabaseURL string
}

// Load reads ADDR and DATABASE_URL.
// ADDR defaults to 127.0.0.1:8080. Any non-loopback host is rejected.
func Load() (Config, error) {
	addr := strings.TrimSpace(os.Getenv(envAddr))
	if addr == "" {
		addr = defaultAddr
	}
	if err := validateAddr(addr); err != nil {
		return Config{}, err
	}
	dsn := strings.TrimSpace(os.Getenv(envDatabase))
	if dsn == "" {
		return Config{}, fmt.Errorf("%s is not set", envDatabase)
	}
	return Config{Addr: addr, DatabaseURL: dsn}, nil
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ADDR: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("ADDR port %q is invalid", port)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("ADDR host %q is not a loopback address", host)
	}
	return nil
}
