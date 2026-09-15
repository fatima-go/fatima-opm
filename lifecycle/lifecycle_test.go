package lifecycle

import "testing"

func TestUncertainWorkMustRemainReserved(t *testing.T) {
	for _, state := range []string{"RUNNING", "WAITING", "ATTENTION", "", "UNKNOWN"} {
		if Terminal(state) {
			t.Fatalf("unsafe release for %q", state)
		}
	}
	for _, state := range []string{"FAILED", "SUCCEEDED", "CANCELLED", "EXPIRED"} {
		if !Terminal(state) {
			t.Fatalf("finished rollout blocks new work: %s", state)
		}
	}
	for _, state := range []string{"QUEUED", "STAGING", "STAGED", "RUNNING", ""} {
		if OperationTerminal(state) {
			t.Fatalf("operation may still start or run: %s", state)
		}
	}
}
