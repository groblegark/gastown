#!/bin/bash
#
# decision-notify.sh - macOS notification with actionable yes/no buttons
#
# Uses alerter (recommended) or falls back to alternatives for displaying
# macOS notifications with action buttons that return user responses.
#
# Dependencies (one of):
#   - alerter: brew install alerter (or download from github.com/vjeantet/alerter)
#   - NotifiCLI: github.com/saihgupr/NotifiCLI
#
# Usage:
#   decision-notify.sh yesno "Approve this change?"
#   decision-notify.sh yesno "Deploy to prod?" --default=no --timeout=30
#   decision-notify.sh choice "Select environment:" staging production development
#
# Output:
#   Prints the user's response to stdout: "yes", "no", "timeout", or selected option
#
# Exit codes:
#   0 - User made a selection
#   1 - Error (no notification tool available, invalid args)
#   2 - Timeout occurred
#   3 - User dismissed without selection

set -euo pipefail

# Configuration
DEFAULT_TIMEOUT=0  # 0 = no timeout
TITLE="${GT_NOTIFY_TITLE:-Claude Code}"
SUBTITLE="${GT_NOTIFY_SUBTITLE:-Decision Required}"
SOUND="${GT_NOTIFY_SOUND:-default}"  # "default", "Ping", "Basso", etc. or empty for none

# Detect available notification tool
detect_tool() {
    if command -v alerter &>/dev/null; then
        echo "alerter"
    elif command -v notificli &>/dev/null; then
        echo "notificli"
    elif [[ "$(uname)" == "Darwin" ]]; then
        # macOS but no tool installed - provide guidance
        echo "none"
    else
        # Not macOS
        echo "unsupported"
    fi
}

# Show error and exit
error() {
    echo "ERROR: $1" >&2
    exit 1
}

# Show yes/no notification using alerter
alerter_yesno() {
    local message="$1"
    local default="${2:-}"
    local timeout="${3:-$DEFAULT_TIMEOUT}"

    local args=(
        -title "$TITLE"
        -subtitle "$SUBTITLE"
        -message "$message"
        -actions "Yes"
        -closeLabel "No"
    )

    if [[ "$timeout" -gt 0 ]]; then
        args+=(-timeout "$timeout")
    fi

    if [[ -n "$SOUND" && "$SOUND" != "none" ]]; then
        args+=(-sound "$SOUND")
    fi

    local result
    result=$(alerter "${args[@]}" 2>/dev/null) || true

    case "$result" in
        "Yes")
            echo "yes"
            exit 0
            ;;
        "@CLOSED"|"No")
            echo "no"
            exit 0
            ;;
        "@TIMEOUT")
            if [[ -n "$default" ]]; then
                echo "$default"
            else
                echo "timeout"
            fi
            exit 2
            ;;
        "@CONTENTCLICKED")
            # User clicked notification body - treat as wanting to engage
            # Re-show or treat as needing terminal interaction
            echo "clicked"
            exit 0
            ;;
        "@ACTIONCLICKED")
            echo "yes"
            exit 0
            ;;
        *)
            # Unknown response - check if it matches an action
            if [[ "$result" == "Yes" ]]; then
                echo "yes"
            else
                echo "no"
            fi
            exit 0
            ;;
    esac
}

# Show choice notification using alerter
alerter_choice() {
    local message="$1"
    local default="$2"
    local timeout="$3"
    shift 3
    local options=("$@")

    # alerter uses comma-separated actions for dropdown
    local actions_str
    actions_str=$(IFS=,; echo "${options[*]}")

    local args=(
        -title "$TITLE"
        -subtitle "$SUBTITLE"
        -message "$message"
        -actions "$actions_str"
        -closeLabel "Cancel"
    )

    if [[ "$timeout" -gt 0 ]]; then
        args+=(-timeout "$timeout")
    fi

    if [[ -n "$SOUND" && "$SOUND" != "none" ]]; then
        args+=(-sound "$SOUND")
    fi

    local result
    result=$(alerter "${args[@]}" 2>/dev/null) || true

    case "$result" in
        "@CLOSED")
            echo "cancelled"
            exit 3
            ;;
        "@TIMEOUT")
            if [[ -n "$default" ]]; then
                echo "$default"
            else
                echo "timeout"
            fi
            exit 2
            ;;
        "@CONTENTCLICKED")
            echo "clicked"
            exit 0
            ;;
        *)
            # Return the selected option
            echo "$result"
            exit 0
            ;;
    esac
}

# Show yes/no notification using NotifiCLI
notificli_yesno() {
    local message="$1"
    local default="${2:-}"
    local timeout="${3:-$DEFAULT_TIMEOUT}"

    local args=(
        -title "$TITLE"
        -subtitle "$SUBTITLE"
        -message "$message"
        -actions "Yes,No"
        -persistent
    )

    if [[ "$timeout" -gt 0 ]]; then
        args+=(-timeout "$timeout")
    fi

    local result
    result=$(notificli "${args[@]}" 2>/dev/null) || true

    case "$result" in
        "Yes")
            echo "yes"
            exit 0
            ;;
        "No"|"dismissed")
            echo "no"
            exit 0
            ;;
        "timeout")
            if [[ -n "$default" ]]; then
                echo "$default"
            else
                echo "timeout"
            fi
            exit 2
            ;;
        *)
            echo "no"
            exit 0
            ;;
    esac
}

# Show choice notification using NotifiCLI
notificli_choice() {
    local message="$1"
    local default="$2"
    local timeout="$3"
    shift 3
    local options=("$@")

    local actions_str
    actions_str=$(IFS=,; echo "${options[*]}")

    local args=(
        -title "$TITLE"
        -subtitle "$SUBTITLE"
        -message "$message"
        -actions "$actions_str"
        -persistent
    )

    if [[ "$timeout" -gt 0 ]]; then
        args+=(-timeout "$timeout")
    fi

    local result
    result=$(notificli "${args[@]}" 2>/dev/null) || true

    case "$result" in
        "dismissed")
            echo "cancelled"
            exit 3
            ;;
        "timeout")
            if [[ -n "$default" ]]; then
                echo "$default"
            else
                echo "timeout"
            fi
            exit 2
            ;;
        *)
            echo "$result"
            exit 0
            ;;
    esac
}

# Fallback: use osascript dialog (blocking, less nice UX)
osascript_yesno() {
    local message="$1"
    local default="${2:-no}"
    local timeout="${3:-0}"

    local default_button
    if [[ "$default" == "yes" ]]; then
        default_button="Yes"
    else
        default_button="No"
    fi

    local script="display dialog \"$message\" with title \"$TITLE\" buttons {\"No\", \"Yes\"} default button \"$default_button\""

    if [[ "$timeout" -gt 0 ]]; then
        script="$script giving up after $timeout"
    fi

    local result
    result=$(osascript -e "$script" 2>/dev/null) || {
        # User cancelled
        echo "no"
        exit 3
    }

    if [[ "$result" == *"gave up:true"* ]]; then
        echo "$default"
        exit 2
    elif [[ "$result" == *"Yes"* ]]; then
        echo "yes"
        exit 0
    else
        echo "no"
        exit 0
    fi
}

# Fallback: use osascript for choice
osascript_choice() {
    local message="$1"
    local default="$2"
    local timeout="$3"
    shift 3
    local options=("$@")

    # Build AppleScript list
    local opts_str=""
    for opt in "${options[@]}"; do
        if [[ -n "$opts_str" ]]; then
            opts_str="$opts_str, \"$opt\""
        else
            opts_str="\"$opt\""
        fi
    done

    local script="choose from list {$opts_str} with title \"$TITLE\" with prompt \"$message\""
    if [[ -n "$default" ]]; then
        script="$script default items {\"$default\"}"
    fi

    local result
    result=$(osascript -e "$script" 2>/dev/null) || {
        echo "cancelled"
        exit 3
    }

    if [[ "$result" == "false" ]]; then
        echo "cancelled"
        exit 3
    else
        echo "$result"
        exit 0
    fi
}

# Parse arguments
parse_args() {
    local -n _default=$1
    local -n _timeout=$2
    shift 2

    _default=""
    _timeout="$DEFAULT_TIMEOUT"

    local positional=()
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --default=*)
                _default="${1#*=}"
                shift
                ;;
            --timeout=*)
                _timeout="${1#*=}"
                shift
                ;;
            -d)
                _default="$2"
                shift 2
                ;;
            -t)
                _timeout="$2"
                shift 2
                ;;
            *)
                positional+=("$1")
                shift
                ;;
        esac
    done

    # Return positional args
    echo "${positional[@]}"
}

# Main: yes/no decision
do_yesno() {
    local default="" timeout=""
    local args
    args=$(parse_args default timeout "$@")
    local message="$args"

    if [[ -z "$message" ]]; then
        error "Usage: $0 yesno <message> [--default=yes|no] [--timeout=N]"
    fi

    local tool
    tool=$(detect_tool)

    case "$tool" in
        alerter)
            alerter_yesno "$message" "$default" "$timeout"
            ;;
        notificli)
            notificli_yesno "$message" "$default" "$timeout"
            ;;
        none)
            # Fall back to osascript dialog
            osascript_yesno "$message" "$default" "$timeout"
            ;;
        unsupported)
            error "macOS notification tools not available (not on macOS or missing alerter/notificli)"
            ;;
    esac
}

# Main: choice decision
do_choice() {
    local default="" timeout=""

    # Parse --default and --timeout from args
    local message=""
    local options=()

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --default=*)
                default="${1#*=}"
                shift
                ;;
            --timeout=*)
                timeout="${1#*=}"
                shift
                ;;
            -d)
                default="$2"
                shift 2
                ;;
            -t)
                timeout="$2"
                shift 2
                ;;
            *)
                if [[ -z "$message" ]]; then
                    message="$1"
                else
                    options+=("$1")
                fi
                shift
                ;;
        esac
    done

    timeout="${timeout:-$DEFAULT_TIMEOUT}"

    if [[ -z "$message" || ${#options[@]} -lt 2 ]]; then
        error "Usage: $0 choice <message> <option1> <option2> [option3...] [--default=X] [--timeout=N]"
    fi

    local tool
    tool=$(detect_tool)

    case "$tool" in
        alerter)
            alerter_choice "$message" "$default" "$timeout" "${options[@]}"
            ;;
        notificli)
            notificli_choice "$message" "$default" "$timeout" "${options[@]}"
            ;;
        none)
            osascript_choice "$message" "$default" "$timeout" "${options[@]}"
            ;;
        unsupported)
            error "macOS notification tools not available (not on macOS or missing alerter/notificli)"
            ;;
    esac
}

# Check tool availability
do_check() {
    local tool
    tool=$(detect_tool)

    case "$tool" in
        alerter)
            echo "alerter (recommended)"
            alerter -help 2>&1 | head -1 || true
            exit 0
            ;;
        notificli)
            echo "NotifiCLI"
            exit 0
            ;;
        none)
            echo "No notification tool found. Install one of:"
            echo "  brew install alerter"
            echo "  # or download from https://github.com/vjeantet/alerter/releases"
            echo ""
            echo "Fallback: osascript dialogs (less elegant)"
            exit 1
            ;;
        unsupported)
            echo "Not running on macOS - notifications not supported"
            exit 1
            ;;
    esac
}

# Usage
usage() {
    cat <<'EOF'
Usage: decision-notify.sh <command> [args...]

Commands:
    yesno <message> [options]     Show yes/no notification
    choice <message> <opts...>    Show choice notification with options
    check                         Check available notification tools

Options:
    --default=VALUE    Default value if timeout occurs
    --timeout=SECONDS  Auto-dismiss after N seconds (0 = never)
    -d VALUE           Short form of --default
    -t SECONDS         Short form of --timeout

Environment:
    GT_NOTIFY_TITLE     Notification title (default: "Claude Code")
    GT_NOTIFY_SUBTITLE  Notification subtitle (default: "Decision Required")
    GT_NOTIFY_SOUND     Sound name or "none" (default: "default")

Examples:
    # Simple yes/no
    decision-notify.sh yesno "Approve changes to auth.ts?"

    # Yes/no with timeout and default
    decision-notify.sh yesno "Deploy to production?" --default=no --timeout=30

    # Multiple choice
    decision-notify.sh choice "Select target:" staging production development

Exit codes:
    0 - User made a selection
    1 - Error (no tool, invalid args)
    2 - Timeout occurred
    3 - User dismissed/cancelled

Dependencies:
    Requires one of (in order of preference):
    - alerter: https://github.com/vjeantet/alerter (recommended)
    - NotifiCLI: https://github.com/saihgupr/NotifiCLI
    - osascript: Built-in macOS (fallback, uses modal dialogs)
EOF
}

# Main entry
case "${1:-}" in
    yesno)
        shift
        do_yesno "$@"
        ;;
    choice)
        shift
        do_choice "$@"
        ;;
    check)
        do_check
        ;;
    help|--help|-h)
        usage
        ;;
    "")
        usage
        exit 1
        ;;
    *)
        error "Unknown command: $1. Use 'help' for usage."
        ;;
esac
