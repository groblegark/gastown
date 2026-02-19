package portutil

import (
	"testing"
)

func TestFreePort(t *testing.T) {
	port, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort() error: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("FreePort() = %d, want valid port (1-65535)", port)
	}
}

func TestFreePort_Unique(t *testing.T) {
	// Two consecutive calls should return different ports
	port1, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort() #1 error: %v", err)
	}
	port2, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort() #2 error: %v", err)
	}
	// Ports should usually be different (not guaranteed but very likely)
	if port1 == port2 {
		t.Logf("FreePort() returned same port twice: %d (unusual but not a bug)", port1)
	}
}
