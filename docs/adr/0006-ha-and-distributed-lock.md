# ADR 6: HA Topology and Distributed Locking — Deferred Until Deployment Target

## Status: Placeholder (2026-05-11)

## Context

R3 (ADR-0005) closed the in-process refactoring backlog: file splits,
fuzz tests, benchmarks, OTel, and the gin router. Two operational
concerns remain unaddressed:

1. **Multi-replica deployment.** `pkg/api.Runtime` currently keeps
   per-session history in-process via `historyStore` and serializes
   per-session work via `sessionGate` (`pkg/api/runtime_helpers.go`).
   Both are `sync.Map` / mutex-backed — fine for a single replica but
   wrong if the same `sessionID` lands on two replicas behind a load
   balancer (the in-process gate doesn't see the other holder, and
   either replica's history will lag the other).

2. **Background jobs and cron.** R2 added `sessionGate.cleanupLoop`
   and `bashOutputSessionDir` cleanup that assume the local filesystem
   is the source of truth. A multi-replica deployment needs either
   shared object storage or a leader election so only one replica
   runs the cleanup.

R3 explicitly **does not** introduce a distributed lock manager,
session-affinity routing, or shared-state backend, because the choice
is load-bearing on the deployment target and that target is not yet
fixed:

- **Single-tenant on-prem (today).** One replica per tenant, local
  disk, local lock — current code is correct.
- **Multi-tenant SaaS.** Need shared storage (S3/GCS) for sessions,
  Redis-backed lock for `sessionGate`, and either sticky-session
  routing or a session router that pins each `sessionID` to one
  replica.
- **Hybrid (per-org dedicated, per-user shared).** Need both: local
  fast path for dedicated orgs, shared-state path for the multi-tenant
  pool. Likely picks the most expensive design from each.

Choosing one of these prematurely means either over-engineering
(deploying Redis to support a single-tenant on-prem install) or
under-designing (shipping a SaaS replica fleet that double-runs cron
jobs and corrupts session history).

## Decision

**Defer.** Specifically:

1. Do not add distributed-lock dependencies (etcd, Redis, Consul,
   ZooKeeper) to `go.mod` until a deployment target is committed.
2. Do not introduce a shared-state interface seam in `historyStore`
   or `sessionGate` ahead of need — premature interface seams encode
   today's wrong assumptions.
3. Document the constraint loudly: any operator deploying >1 replica
   today must use sticky-session routing keyed on `sessionID` (or
   `X-Saker-Session-ID` header). README / deployment.md should
   reflect this when this ADR moves from Placeholder to Accepted.

## When to revisit

Reopen this ADR when **any** of the following lands:

- A concrete SaaS deployment target with replica count > 1.
- A customer requirement for cross-replica session resumption.
- A measured cron-collision incident (two replicas running
  `sessionGate.cleanupLoop` against shared storage).

At that point pick a lock backend (likely Redis if Kubernetes, etcd
if running alongside one), define the seam in `pkg/api`, and migrate
`historyStore` to the chosen storage with a feature flag for the
old in-process path so single-tenant operators are unaffected.

## Consequences

### Positive

- **No premature dependency.** `go.mod` stays lean; on-prem
  installations don't need to deploy a Redis cluster they won't use.
- **No premature interface.** When the deployment target is fixed,
  the right seam can be designed against real constraints rather
  than guessed at.

### Negative

- **HA is not free today.** Operators wanting >1 replica must
  configure sticky-session routing themselves; misconfiguration
  silently corrupts session state.
- **Decision deferred, not eliminated.** The next operational round
  will pay the full cost of choosing and integrating a backend.

### Pointers

- `pkg/api/runtime_helpers.go` — `historyStore`, `sessionGate`,
  `cleanupLoop` (in-process state today)
- `pkg/api/request_response.go` — `SessionID` field; sticky-routing
  key
- ADR-0001 — gin migration (router layer where session-affinity
  middleware would attach)
- ADR-0005 — R3 round; the work that exposed the deferred items
