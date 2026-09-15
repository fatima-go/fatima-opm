# Deployment owner lifecycle

This additive v2 API separates a creating CLI's owner session from progress Watch
streams. `deployment_management` on Jupiter and `deployment_cancel` on every
selected Juno are required before submitting a managed rollout. Older clients
can still create unmanaged rollouts; opening an observer never adopts them.

- Open authenticates an OPERATOR and returns a random ID and bearer secret.
- The CLI keeps these credentials for the lifetime of its process, including
  reconnects and screen changes. Only Open returns the secret. Never log it.
- Create binds the credential atomically to one rollout. A repeated Create with
  the same request ID/contents recovers its result without creating more work.
- Heartbeat is sent every 10 seconds. At 20 seconds since last receipt the owner
  expires; a late heartbeat cannot resurrect it. Server scheduling may delay
  visibility of expiry, but heartbeat and dispatch must recheck the deadline.
- Detach explicitly closes ownership without waiting for the timeout. Normal
  exit attempts Detach; a lost request is covered by expiry. Observers send none.
- Owner loss cancels future work; it does not kill an executing process. Preserve
  per-target results. Automatically reconcile uncertain execution using the same
  operation IDs. Failures confirmed to have ended are terminal, not live locks.
- Cancel(OperationSpec) on Juno authenticates the scoped deployment ticket and
  durably seals even an absent operation ID. Stage/Start racing it must either
  observe CANCELLED or return a started operation whose result must be tracked.
- No deadline alone can prove an executing operation stopped. Persist the cancel
  intent and retry reconciliation if Juno is unavailable.
- Structured RolloutConflict status details identify a blocking rollout. Status
  snapshots expose last/next reconciliation times and whether new work is blocked.

Coordinated source builds use the sibling fatima-core checkout until a release of
this API is published. Generate bindings from the repository root:

    protoc -I . --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. api/control.proto

Rollout.next_target_start_at is the persisted next-target start deadline in Unix milliseconds (0 means no scheduled gap). It is independent of next_check_at, which is for reconciliation.
