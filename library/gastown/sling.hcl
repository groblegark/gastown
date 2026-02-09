# @gt-library: gastown/sling
# @gt-version: 1.0.0
#
# GT Sling runbook — OJ-managed polecat lifecycle.
# Dispatched by `gt sling` when GT_SLING_OJ=1.
#
# GT owns: name allocation, formula instantiation, bead lifecycle.
# OJ owns: workspace setup, agent spawn, step execution, crash recovery, cleanup.
#
# Variables (passed by gt sling via --var):
#   issue         - Bead ID for the work item
#   instructions  - Task instructions (bead title)
#   base          - Base branch (default: main)
#   rig           - Rig name
#   polecat_name  - Allocated polecat name
#   town_root     - Gas Town root directory
#   env_json      - Base64-encoded agent environment map

job "gt-sling" {
  name      = "Sling: ${var.issue}"
  vars      = ["issue", "instructions", "base", "rig", "polecat_name", "town_root", "env_json"]
  on_fail   = { step = "cleanup" }
  on_cancel = { step = "cleanup" }

  workspace {
    git    = "worktree"
    branch = "${var.polecat_name}/${workspace.nonce}"
    base   = "${var.base}"
    dir    = "${var.town_root}"
  }

  locals {
    agent_id = "${var.rig}/polecats/${var.polecat_name}"
  }

  notify {
    on_start = "Sling: ${var.issue} on ${local.agent_id}"
    on_done  = "Done: ${var.issue}"
    on_fail  = "Failed: ${var.issue}"
  }

  step "setup" {
    run = <<-SHELL
      # Decode agent env and export into workspace
      echo "${var.env_json}" | base64 -d > "${workspace.root}/.agent-env.json"

      # Update bead status to in_progress
      bd update "${var.issue}" --status=in_progress 2>/dev/null || true

      # Emit bus event: job started
      bd bus emit OjJobCreated --json \
        '{"job_id":"${job.id}","job_name":"Sling: ${var.issue}","bead_id":"${var.issue}"}' \
        2>/dev/null || true
    SHELL
    on_done = { step = "work" }
  }

  step "work" {
    run     = { agent = "polecat" }
    on_done = { step = "submit" }
  }

  step "submit" {
    run = <<-SHELL
      cd "${workspace.root}"

      # Stage and commit any remaining changes
      git add -A
      if ! git diff --cached --quiet; then
        git commit -m "work: ${var.issue}"
      fi

      # Push if there are commits beyond base
      if test "$(git rev-list --count HEAD ^origin/${var.base})" -gt 0; then
        branch="${workspace.branch}"
        git push origin "$branch"
        echo "Pushed to $branch"
      else
        echo "No changes to push"
      fi

      # Signal gt done (creates MR bead, enters merge queue)
      cd "${var.town_root}"
      GT_RIG="${var.rig}" gt done "${var.issue}" --polecat="${var.polecat_name}" 2>&1 || true
    SHELL
    on_done = { step = "cleanup" }
  }

  step "cleanup" {
    run = <<-SHELL
      # Clean up workspace
      rm -f "${workspace.root}/.agent-env.json" 2>/dev/null || true

      # Emit bus event: job completed or failed
      if [ "${job.status}" = "done" ]; then
        bd bus emit OjJobCompleted --json \
          '{"job_id":"${job.id}","job_name":"Sling: ${var.issue}","bead_id":"${var.issue}"}' \
          2>/dev/null || true
      else
        bd bus emit OjJobFailed --json \
          '{"job_id":"${job.id}","job_name":"Sling: ${var.issue}","bead_id":"${var.issue}","error":"${job.error}"}' \
          2>/dev/null || true
      fi
    SHELL
  }
}

agent "polecat" {
  run     = "claude --model opus --dangerously-skip-permissions"
  env     = { file = "${workspace.root}/.agent-env.json" }
  on_dead = { action = "resume", attempts = 2 }

  on_idle {
    action  = "nudge"
    message = <<-MSG
      Keep working on the task. When finished, commit your changes.
      Verify with the project's test/check command before committing.
    MSG
  }

  session "tmux" {
    color = "green"
    title = "Polecat: ${var.polecat_name}"
    status {
      left  = "${var.issue}: ${var.instructions}"
      right = "${var.rig}/${var.polecat_name}"
    }
  }

  prime = [
    "echo '## Issue'",
    "bd show ${var.issue}",
    "echo '## Git Status'",
    "git status",
    "echo '## Workflow'",
    "echo '1. Implement the task described in the issue'",
    "echo '2. Write or update tests'",
    "echo '3. Verify: run the project check/test command'",
    "echo '4. Commit your changes'",
  ]

  prompt = "${var.instructions}"
}
