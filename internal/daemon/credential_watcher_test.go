package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCredentialEvent_Parsing(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantType  string
		wantAcct  string
		wantError string
	}{
		{
			name:      "refresh_failed",
			input:     `{"event_type":"refresh_failed","account":"personal","error":"invalid_grant","ts":"2026-02-12T03:00:00Z"}`,
			wantType:  "refresh_failed",
			wantAcct:  "personal",
			wantError: "invalid_grant",
		},
		{
			name:     "refreshed",
			input:    `{"event_type":"refreshed","account":"personal","ts":"2026-02-12T03:05:00Z"}`,
			wantType: "refreshed",
			wantAcct: "personal",
		},
		{
			name:     "reauth_required",
			input:    `{"event_type":"reauth_required","account":"personal","ts":"2026-02-12T03:05:00Z"}`,
			wantType: "reauth_required",
			wantAcct: "personal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event CredentialEvent
			if err := json.Unmarshal([]byte(tt.input), &event); err != nil {
				t.Fatalf("failed to parse: %v", err)
			}
			if event.EventType != tt.wantType {
				t.Errorf("EventType = %q, want %q", event.EventType, tt.wantType)
			}
			if event.Account != tt.wantAcct {
				t.Errorf("Account = %q, want %q", event.Account, tt.wantAcct)
			}
			if event.Error != tt.wantError {
				t.Errorf("Error = %q, want %q", event.Error, tt.wantError)
			}
		})
	}
}

func TestCredentialWatcher_HandleMessage(t *testing.T) {
	var logs []string
	logger := func(format string, args ...interface{}) {
		logs = append(logs, format)
	}

	w := &CredentialWatcher{
		logger: logger,
		daemon: &Daemon{config: &Config{TownRoot: t.TempDir()}},
	}

	tests := []struct {
		name       string
		payload    string
		wantLogKey string // substring expected in log messages
	}{
		{
			name:       "refresh_failed logs warning",
			payload:    `{"event_type":"refresh_failed","account":"personal","error":"invalid_grant","ts":"2026-02-12T03:00:00Z"}`,
			wantLogKey: "WARNING: credential refresh failed",
		},
		{
			name:       "reauth_required logs warning",
			payload:    `{"event_type":"reauth_required","account":"personal","ts":"2026-02-12T03:05:00Z"}`,
			wantLogKey: "WARNING: re-auth required",
		},
		{
			name:       "refreshed triggers restart",
			payload:    `{"event_type":"refreshed","account":"personal","ts":"2026-02-12T03:05:00Z"}`,
			wantLogKey: "credentials refreshed",
		},
		{
			name:       "unknown event type",
			payload:    `{"event_type":"unknown_thing","account":"personal","ts":"2026-02-12T03:05:00Z"}`,
			wantLogKey: "unhandled credential event type",
		},
		{
			name:       "invalid json",
			payload:    `not json at all`,
			wantLogKey: "error parsing credential event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs = nil
			w.handleMessage([]byte(tt.payload))
			if len(logs) == 0 {
				t.Fatal("expected at least one log message")
			}
			found := false
			for _, l := range logs {
				if strings.Contains(l, tt.wantLogKey) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected log containing %q, got %v", tt.wantLogKey, logs)
			}
		})
	}
}

func TestGetNATSURL(t *testing.T) {
	// Save and restore env
	origURL, hadURL := os.LookupEnv("BD_NATS_URL")
	origPort, hadPort := os.LookupEnv("BD_NATS_PORT")
	defer func() {
		if hadURL {
			os.Setenv("BD_NATS_URL", origURL)
		} else {
			os.Unsetenv("BD_NATS_URL")
		}
		if hadPort {
			os.Setenv("BD_NATS_PORT", origPort)
		} else {
			os.Unsetenv("BD_NATS_PORT")
		}
	}()

	// Test explicit URL
	os.Setenv("BD_NATS_URL", "nats://custom:4222")
	os.Unsetenv("BD_NATS_PORT")
	if got := getNATSURL(); got != "nats://custom:4222" {
		t.Errorf("expected nats://custom:4222, got %q", got)
	}

	// Test port-based URL
	os.Unsetenv("BD_NATS_URL")
	os.Setenv("BD_NATS_PORT", "5222")
	if got := getNATSURL(); got != "nats://localhost:5222" {
		t.Errorf("expected nats://localhost:5222, got %q", got)
	}

	// Test default
	os.Unsetenv("BD_NATS_URL")
	os.Unsetenv("BD_NATS_PORT")
	if got := getNATSURL(); got != "nats://localhost:4222" {
		t.Errorf("expected nats://localhost:4222, got %q", got)
	}
}
