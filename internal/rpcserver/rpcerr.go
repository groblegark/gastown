package rpcserver

import (
	"fmt"
	"log"
	"strings"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/types/known/structpb"
)

// rpcError creates a connect.Error with structured error details attached.
// The details map is serialized as a structpb.Struct and added via AddDetail.
func rpcError(code connect.Code, msg string, details map[string]interface{}) *connect.Error {
	err := connect.NewError(code, fmt.Errorf("%s", msg))
	if len(details) > 0 {
		s, sErr := structpb.NewStruct(details)
		if sErr == nil {
			detail, dErr := connect.NewErrorDetail(s)
			if dErr == nil {
				err.AddDetail(detail)
			}
		}
	}
	return err
}

// invalidArg returns CodeInvalidArgument with field-level detail.
func invalidArg(field, reason string) *connect.Error {
	return rpcError(connect.CodeInvalidArgument,
		fmt.Sprintf("%s: %s", field, reason),
		map[string]interface{}{"field": field, "reason": reason},
	)
}

// notFound returns CodeNotFound with resource identification.
func notFound(resource, id string) *connect.Error {
	return rpcError(connect.CodeNotFound,
		fmt.Sprintf("%s not found: %s", resource, id),
		map[string]interface{}{"resource": resource, "id": id},
	)
}

// internalErr returns CodeInternal for unexpected failures.
// The raw error is logged server-side but not exposed to clients.
func internalErr(msg string, err error) *connect.Error {
	if err != nil {
		log.Printf("RPC internal error: %s: %v", msg, err)
	}
	return rpcError(connect.CodeInternal, msg, nil)
}

// unavailableWithRetry returns CodeUnavailable with retry guidance.
// Use for transient failures where retrying may succeed (command execution,
// temporary resource unavailability, etc).
func unavailableWithRetry(msg string, retryAfterSec int) *connect.Error {
	return rpcError(connect.CodeUnavailable, msg,
		map[string]interface{}{
			"retryable":           true,
			"retry_after_seconds": float64(retryAfterSec),
		},
	)
}

// cmdError handles command execution failures. It logs the full output
// server-side for debugging but returns a sanitized message to clients.
// Transient failures get CodeUnavailable with retry guidance.
func cmdError(operation string, err error, output []byte) *connect.Error {
	sanitized := sanitizeOutput(string(output))
	log.Printf("RPC command error [%s]: %v\nOutput: %s", operation, err, string(output))

	// Check for clearly transient patterns
	outStr := string(output)
	if isTransientError(outStr, err) {
		return rpcError(connect.CodeUnavailable,
			fmt.Sprintf("%s: temporarily unavailable, please retry", operation),
			map[string]interface{}{
				"retryable":           true,
				"retry_after_seconds": float64(5),
				"operation":           operation,
				"hint":                sanitized,
			},
		)
	}

	return rpcError(connect.CodeInternal,
		fmt.Sprintf("%s failed", operation),
		map[string]interface{}{
			"operation": operation,
			"hint":      sanitized,
		},
	)
}

// isTransientError checks if a command failure looks transient.
func isTransientError(output string, err error) bool {
	transientPatterns := []string{
		"timeout", "timed out",
		"connection refused", "connection reset",
		"no such session", "session not found",
		"resource temporarily unavailable",
		"lock", "locked",
		"too many open files",
		"temporary failure",
	}
	lower := strings.ToLower(output + " " + err.Error())
	for _, p := range transientPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// sanitizeOutput strips sensitive internal details from command output
// for safe inclusion in client-facing error messages.
func sanitizeOutput(output string) string {
	// Truncate long output
	const maxLen = 200
	output = strings.TrimSpace(output)

	// Strip home directory paths
	for _, prefix := range []string{"/home/ubuntu/", "/tmp/"} {
		for strings.Contains(output, prefix) {
			idx := strings.Index(output, prefix)
			// Find end of path (next space or newline)
			end := idx
			for end < len(output) && output[end] != ' ' && output[end] != '\n' && output[end] != '\t' {
				end++
			}
			output = output[:idx] + "<path>" + output[end:]
		}
	}

	if len(output) > maxLen {
		output = output[:maxLen] + "..."
	}
	return output
}
