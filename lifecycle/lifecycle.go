// Package lifecycle defines the shared deployment management contract.
package lifecycle

import "time"

const (
	ManagementFeature = "deployment_management"
	CancelFeature     = "deployment_cancel"
	HeartbeatInterval = 10 * time.Second
	OwnerTimeout      = 20 * time.Second
)

// Terminal means the coordinator has reconciled all dispatched operations.
// Failure history remains queryable but must not reserve a new deployment.
func Terminal(state string) bool {
	switch state {
	case "SUCCEEDED", "FAILED", "CANCELLED", "EXPIRED":
		return true
	}
	return false
}

func OperationTerminal(state string) bool {
	switch state {
	case "SUCCEEDED", "FAILED", "INTERRUPTED", "CANCELLED", "DRIFTED":
		return true
	}
	return false
}
