package flow

import gobellim "github.com/KirillYuzh/Gobellim"

// Store makes resume single-use across replicas. Claim must be atomic: exactly
// one caller may claim a nonce. A non-nil error is also a failed claim.
//
// A flow without an explicit Store uses a process-local MemStore, so a token
// cannot be resumed twice in one replica. Sharing the claim between replicas
// still needs a real Store such as store/sql.
//
// Store is the same interface as gobellim.ClaimStore, so one store can protect
// both a resume and a Task.ExecuteApproved.
type Store = gobellim.ClaimStore

// MemStore is an in-process Store for tests and single-replica deployments.
// An expired nonce may be claimed again; Prune is not required.
type MemStore = gobellim.MemClaims

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return gobellim.NewMemClaims() }
