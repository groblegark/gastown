package beadswatcher

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestStubWatcher_Events(t *testing.T) {
	w := NewStubWatcher(slog.Default())
	ch := w.Events()
	if ch == nil {
		t.Fatal("Events() returned nil channel")
	}
}

func TestStubWatcher_Start(t *testing.T) {
	w := NewStubWatcher(slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := w.Start(ctx)
	if err == nil {
		t.Fatal("Start() should return error when context canceled")
	}
}

func TestEventTypes(t *testing.T) {
	types := []EventType{AgentSpawn, AgentDone, AgentStuck, AgentKill}
	for _, et := range types {
		if et == "" {
			t.Error("event type should not be empty")
		}
	}
}
