package events

import (
	"testing"
)

func TestPayloadHelpers(t *testing.T) {
	t.Run("SlingPayload", func(t *testing.T) {
		p := SlingPayload("bd-123", "gastown/polecats/nux")
		if p["bead"] != "bd-123" {
			t.Errorf("bead = %v, want bd-123", p["bead"])
		}
		if p["target"] != "gastown/polecats/nux" {
			t.Errorf("target = %v, want gastown/polecats/nux", p["target"])
		}
		if len(p) != 2 {
			t.Errorf("len = %d, want 2", len(p))
		}
	})

	t.Run("HookPayload", func(t *testing.T) {
		p := HookPayload("bd-456")
		if p["bead"] != "bd-456" {
			t.Errorf("bead = %v, want bd-456", p["bead"])
		}
		if len(p) != 1 {
			t.Errorf("len = %d, want 1", len(p))
		}
	})

	t.Run("UnhookPayload", func(t *testing.T) {
		p := UnhookPayload("bd-789")
		if p["bead"] != "bd-789" {
			t.Errorf("bead = %v, want bd-789", p["bead"])
		}
	})

	t.Run("HandoffPayload with subject", func(t *testing.T) {
		p := HandoffPayload("fix bug", true)
		if p["to_session"] != true {
			t.Errorf("to_session = %v, want true", p["to_session"])
		}
		if p["subject"] != "fix bug" {
			t.Errorf("subject = %v, want fix bug", p["subject"])
		}
	})

	t.Run("HandoffPayload empty subject", func(t *testing.T) {
		p := HandoffPayload("", false)
		if _, ok := p["subject"]; ok {
			t.Errorf("subject should not be present for empty string")
		}
		if p["to_session"] != false {
			t.Errorf("to_session = %v, want false", p["to_session"])
		}
	})

	t.Run("DonePayload", func(t *testing.T) {
		p := DonePayload("bd-abc", "polecat/nux")
		if p["bead"] != "bd-abc" {
			t.Errorf("bead = %v, want bd-abc", p["bead"])
		}
		if p["branch"] != "polecat/nux" {
			t.Errorf("branch = %v, want polecat/nux", p["branch"])
		}
	})

	t.Run("MailPayload", func(t *testing.T) {
		p := MailPayload("gastown/witness", "patrol complete")
		if p["to"] != "gastown/witness" {
			t.Errorf("to = %v, want gastown/witness", p["to"])
		}
		if p["subject"] != "patrol complete" {
			t.Errorf("subject = %v, want patrol complete", p["subject"])
		}
	})

	t.Run("SpawnPayload", func(t *testing.T) {
		p := SpawnPayload("gastown", "nux")
		if p["rig"] != "gastown" {
			t.Errorf("rig = %v, want gastown", p["rig"])
		}
		if p["polecat"] != "nux" {
			t.Errorf("polecat = %v, want nux", p["polecat"])
		}
	})

	t.Run("BootPayload", func(t *testing.T) {
		agents := []string{"witness", "refinery", "deacon"}
		p := BootPayload("gastown", agents)
		if p["rig"] != "gastown" {
			t.Errorf("rig = %v, want gastown", p["rig"])
		}
		gotAgents, ok := p["agents"].([]string)
		if !ok {
			t.Fatalf("agents is not []string")
		}
		if len(gotAgents) != 3 {
			t.Errorf("agents len = %d, want 3", len(gotAgents))
		}
	})

	t.Run("KillPayload", func(t *testing.T) {
		p := KillPayload("gastown", "nux", "zombie cleanup")
		if p["rig"] != "gastown" {
			t.Errorf("rig = %v, want gastown", p["rig"])
		}
		if p["target"] != "nux" {
			t.Errorf("target = %v, want nux", p["target"])
		}
		if p["reason"] != "zombie cleanup" {
			t.Errorf("reason = %v, want zombie cleanup", p["reason"])
		}
	})

	t.Run("HaltPayload", func(t *testing.T) {
		services := []string{"daemon", "witness"}
		p := HaltPayload(services)
		gotServices, ok := p["services"].([]string)
		if !ok {
			t.Fatalf("services is not []string")
		}
		if len(gotServices) != 2 {
			t.Errorf("services len = %d, want 2", len(gotServices))
		}
	})
}

func TestMergePayload(t *testing.T) {
	t.Run("with reason", func(t *testing.T) {
		p := MergePayload("mr-001", "nux", "polecat/nux", "conflict on main.go")
		if p["mr"] != "mr-001" {
			t.Errorf("mr = %v, want mr-001", p["mr"])
		}
		if p["worker"] != "nux" {
			t.Errorf("worker = %v, want nux", p["worker"])
		}
		if p["branch"] != "polecat/nux" {
			t.Errorf("branch = %v, want polecat/nux", p["branch"])
		}
		if p["reason"] != "conflict on main.go" {
			t.Errorf("reason = %v, want conflict on main.go", p["reason"])
		}
	})

	t.Run("without reason", func(t *testing.T) {
		p := MergePayload("mr-002", "dementus", "polecat/dementus", "")
		if _, ok := p["reason"]; ok {
			t.Errorf("reason should not be present for empty string")
		}
	})
}

func TestPatrolPayload(t *testing.T) {
	t.Run("with message", func(t *testing.T) {
		p := PatrolPayload("gastown", 5, "all healthy")
		if p["rig"] != "gastown" {
			t.Errorf("rig = %v, want gastown", p["rig"])
		}
		if p["polecat_count"] != 5 {
			t.Errorf("polecat_count = %v, want 5", p["polecat_count"])
		}
		if p["message"] != "all healthy" {
			t.Errorf("message = %v, want all healthy", p["message"])
		}
	})

	t.Run("without message", func(t *testing.T) {
		p := PatrolPayload("beads", 0, "")
		if _, ok := p["message"]; ok {
			t.Errorf("message should not be present for empty string")
		}
	})
}

func TestPolecatCheckPayload(t *testing.T) {
	t.Run("with issue", func(t *testing.T) {
		p := PolecatCheckPayload("gastown", "nux", "active", "bd-123")
		if p["rig"] != "gastown" {
			t.Errorf("rig = %v, want gastown", p["rig"])
		}
		if p["polecat"] != "nux" {
			t.Errorf("polecat = %v, want nux", p["polecat"])
		}
		if p["status"] != "active" {
			t.Errorf("status = %v, want active", p["status"])
		}
		if p["issue"] != "bd-123" {
			t.Errorf("issue = %v, want bd-123", p["issue"])
		}
	})

	t.Run("without issue", func(t *testing.T) {
		p := PolecatCheckPayload("gastown", "slit", "idle", "")
		if _, ok := p["issue"]; ok {
			t.Errorf("issue should not be present for empty string")
		}
	})
}

func TestNudgePayload(t *testing.T) {
	p := NudgePayload("gastown", "nux", "idle too long")
	if p["rig"] != "gastown" {
		t.Errorf("rig = %v, want gastown", p["rig"])
	}
	if p["target"] != "nux" {
		t.Errorf("target = %v, want nux", p["target"])
	}
	if p["reason"] != "idle too long" {
		t.Errorf("reason = %v, want idle too long", p["reason"])
	}
}

func TestEscalationPayload(t *testing.T) {
	p := EscalationPayload("gastown", "nux", "mayor", "unresponsive after 3 nudges")
	if p["rig"] != "gastown" {
		t.Errorf("rig = %v, want gastown", p["rig"])
	}
	if p["target"] != "nux" {
		t.Errorf("target = %v, want nux", p["target"])
	}
	if p["to"] != "mayor" {
		t.Errorf("to = %v, want mayor", p["to"])
	}
	if p["reason"] != "unresponsive after 3 nudges" {
		t.Errorf("reason = %v, want unresponsive after 3 nudges", p["reason"])
	}
}

func TestSessionDeathPayload(t *testing.T) {
	p := SessionDeathPayload("nux-session", "gastown/polecats/nux", "zombie cleanup", "daemon")
	if p["session"] != "nux-session" {
		t.Errorf("session = %v, want nux-session", p["session"])
	}
	if p["agent"] != "gastown/polecats/nux" {
		t.Errorf("agent = %v, want gastown/polecats/nux", p["agent"])
	}
	if p["reason"] != "zombie cleanup" {
		t.Errorf("reason = %v, want zombie cleanup", p["reason"])
	}
	if p["caller"] != "daemon" {
		t.Errorf("caller = %v, want daemon", p["caller"])
	}
}

func TestMassDeathPayload(t *testing.T) {
	t.Run("with cause", func(t *testing.T) {
		sessions := []string{"nux", "slit", "dementus"}
		p := MassDeathPayload(3, "5s", sessions, "OOM killer")
		if p["count"] != 3 {
			t.Errorf("count = %v, want 3", p["count"])
		}
		if p["window"] != "5s" {
			t.Errorf("window = %v, want 5s", p["window"])
		}
		if p["possible_cause"] != "OOM killer" {
			t.Errorf("possible_cause = %v, want OOM killer", p["possible_cause"])
		}
	})

	t.Run("without cause", func(t *testing.T) {
		p := MassDeathPayload(2, "10s", []string{"a", "b"}, "")
		if _, ok := p["possible_cause"]; ok {
			t.Errorf("possible_cause should not be present for empty string")
		}
	})
}

func TestSessionPayload(t *testing.T) {
	t.Run("with all fields", func(t *testing.T) {
		p := SessionPayload("sess-uuid", "gastown/crew/joe", "working on bd-123", "/home/agent/gt")
		if p["session_id"] != "sess-uuid" {
			t.Errorf("session_id = %v, want sess-uuid", p["session_id"])
		}
		if p["role"] != "gastown/crew/joe" {
			t.Errorf("role = %v, want gastown/crew/joe", p["role"])
		}
		if p["topic"] != "working on bd-123" {
			t.Errorf("topic = %v, want working on bd-123", p["topic"])
		}
		if p["cwd"] != "/home/agent/gt" {
			t.Errorf("cwd = %v, want /home/agent/gt", p["cwd"])
		}
		// actor_pid should be set
		if _, ok := p["actor_pid"]; !ok {
			t.Errorf("actor_pid should be present")
		}
	})

	t.Run("without optional fields", func(t *testing.T) {
		p := SessionPayload("sess-uuid", "deacon", "", "")
		if _, ok := p["topic"]; ok {
			t.Errorf("topic should not be present for empty string")
		}
		if _, ok := p["cwd"]; ok {
			t.Errorf("cwd should not be present for empty string")
		}
	})
}

func TestHookErrorPayload(t *testing.T) {
	t.Run("with stderr", func(t *testing.T) {
		p := HookErrorPayload("SessionStart", "gt prime", 1, "error: config not found", "deacon")
		if p["hook_type"] != "SessionStart" {
			t.Errorf("hook_type = %v, want SessionStart", p["hook_type"])
		}
		if p["command"] != "gt prime" {
			t.Errorf("command = %v, want gt prime", p["command"])
		}
		if p["exit_code"] != 1 {
			t.Errorf("exit_code = %v, want 1", p["exit_code"])
		}
		if p["stderr"] != "error: config not found" {
			t.Errorf("stderr = %v, want error: config not found", p["stderr"])
		}
		if p["role"] != "deacon" {
			t.Errorf("role = %v, want deacon", p["role"])
		}
	})

	t.Run("without stderr", func(t *testing.T) {
		p := HookErrorPayload("Stop", "bd bus emit", 2, "", "witness")
		if _, ok := p["stderr"]; ok {
			t.Errorf("stderr should not be present for empty string")
		}
	})

	t.Run("long stderr truncated", func(t *testing.T) {
		longStderr := string(make([]byte, 600))
		p := HookErrorPayload("Stop", "cmd", 1, longStderr, "witness")
		stderr, ok := p["stderr"].(string)
		if !ok {
			t.Fatalf("stderr is not string")
		}
		if len(stderr) != 503 { // 500 + "..."
			t.Errorf("stderr len = %d, want 503", len(stderr))
		}
	})
}

func TestEventConstants(t *testing.T) {
	// Verify key constants are set correctly (guards against accidental renames)
	if TypeSling != "sling" {
		t.Errorf("TypeSling = %q, want sling", TypeSling)
	}
	if TypeDone != "done" {
		t.Errorf("TypeDone = %q, want done", TypeDone)
	}
	if TypeSessionStart != "session_start" {
		t.Errorf("TypeSessionStart = %q, want session_start", TypeSessionStart)
	}
	if TypeSessionDeath != "session_death" {
		t.Errorf("TypeSessionDeath = %q, want session_death", TypeSessionDeath)
	}
	if BusDecisionCreated != "DecisionCreated" {
		t.Errorf("BusDecisionCreated = %q, want DecisionCreated", BusDecisionCreated)
	}
	if BusMailSent != "MailSent" {
		t.Errorf("BusMailSent = %q, want MailSent", BusMailSent)
	}
	if VisibilityAudit != "audit" {
		t.Errorf("VisibilityAudit = %q, want audit", VisibilityAudit)
	}
	if VisibilityFeed != "feed" {
		t.Errorf("VisibilityFeed = %q, want feed", VisibilityFeed)
	}
	if VisibilityBoth != "both" {
		t.Errorf("VisibilityBoth = %q, want both", VisibilityBoth)
	}
	if EventsFile != ".events.jsonl" {
		t.Errorf("EventsFile = %q, want .events.jsonl", EventsFile)
	}
}
