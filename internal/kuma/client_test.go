package kuma

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestNewClient_TimesOutInsteadOfHanging guards against the fatal
// "all goroutines are asleep - deadlock!" crash caused by passing an
// unbounded context to bremlkuma.New: a stalled endpoint (bad URL, network
// partition, a proxy that mangles long-polling/WebSocket upgrades) would
// otherwise block forever. The listener here accepts TCP connections but
// never writes a response, simulating a stalled Kuma endpoint without
// depending on real network conditions.
func TestNewClient_TimesOutInsteadOfHanging(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept and hold the connection open without ever responding.
			defer conn.Close()
		}
	}()

	const timeout = 300 * time.Millisecond
	start := time.Now()
	_, err = newClient(context.Background(), "http://"+ln.Addr().String(), "user", "pass", timeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("newClient against a stalled endpoint returned nil error, want a timeout error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("newClient took %v to return against a %v timeout — it hung instead of timing out", elapsed, timeout)
	}
}
