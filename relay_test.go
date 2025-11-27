package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigNormalize(t *testing.T) {
	cfg := &Config{
		ListenPort: 0,
		Targets:    []Target{},
	}
	if err := cfg.normalize(); err == nil {
		t.Fatalf("expected error for missing fields")
	}

	cfg = &Config{
		ListenPort: 13121,
		Targets:    []Target{{Host: "127.0.0.1", Port: 49983}},
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BufferSize != 4096 || cfg.StatsInterval != 10 || cfg.LogLevel != "info" {
		t.Fatalf("expected defaults to be populated: %+v", cfg)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := []byte(`{
		"listen_port": 13121,
		"buffer_size": 2048,
		"log_level": "debug",
		"targets": [{"host":"127.0.0.1","port":1234,"name":"Test"}]
	}`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if cfg.BufferSize != 2048 || cfg.LogLevel != "debug" {
		t.Fatalf("config fields not preserved: %+v", cfg)
	}
}

type fakeWriter struct {
	fail bool
}

func (f *fakeWriter) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	if f.fail {
		return 0, os.ErrInvalid
	}
	return len(b), nil
}

func TestForwardPacket(t *testing.T) {
	target := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}
	success := forwardPacket(&fakeWriter{}, []byte("hello"), []*net.UDPAddr{target})
	if success != 1 {
		t.Fatalf("expected 1 success, got %d", success)
	}

	failCount := forwardPacket(&fakeWriter{fail: true}, []byte("hello"), []*net.UDPAddr{target})
	if failCount != 0 {
		t.Fatalf("expected 0 success for failing writer")
	}
}
