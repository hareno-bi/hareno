# S2 Kiban integration spike: findings

Date: 2026-10-13. Gate: PROJECT-PLAN.md Phase 1, S2; ARCHITECTURE-PLAN.md §10.4. Kiban v0.1.2 (image quickstart, `dev/kiban`), driven through its Go SDK (`github.com/rosschiu/kiban/sdk` v0.1.2). Harness: [main.go](main.go). Raw output: [results.txt](results.txt).

The host is a shared 8-core machine with a load average of 8–15 from other projects, so the latency figures are a pessimistic bound, not a capacity estimate.

## Recommendation

**Keep Kiban, with one new blocking condition and two design rules.**

1. **Blocking: Kiban lets one company's position grant access to another company's object.**
   - In Hareno: Hareno's authorization module validates every grant's subject before writing. A position or group must belong to the object's company, which Hareno checks by listing that company's positions and groups.
   - Upstream: [kiban-issues/1-cross-company-grant-subject.md](kiban-issues/1-cross-company-grant-subject.md).
2. **Design rule: single checks are too costly to make on every five-second refresh.**
   - Cache decisions per (subject, company, object, relation) for a fixed TTL; 30 s is proposed.
   - Use `BatchCan` wherever more than one object is checked.
   - This defines the revocation model required by §10.4 item 6: grant revocation, member deactivation and Keycloak disablement all take effect within the TTL.
3. **Design rule: every data-returning request passes a Kiban decision, fresh or cached; a verified token alone is never enough.**
   - Kiban's decision denies a Keycloak-disabled user on the very next check (`KEYCLOAK_DISABLED`), and a deactivated member just as quickly.
   - The access token itself keeps verifying for its remaining lifetime (up to 300 s), so access is cut by the decision step, not by the token.
   - With the decision cache, the disablement-to-access-loss interval equals the cache TTL.
   - A refresh is refused immediately; Hareno's server-side session ends on the first refused refresh.

## Pass criteria

| Criterion | Result |
|---|---|
| Go SDK login and `BatchCan` working | **Pass.** `VerifyUserToken` returns the Keycloak subject and rejects a tampered token. `MeCompanies` lists both of dave's companies. `BatchCan` returns exactly the expected visible set for four users, through position, group and direct grants |
| Check latency at refresh concurrency | **Measured; too slow for a check on every refresh.** `Can`: 29 ms p50 serially, saturating at about 90–110 checks/s, with p95 near 0.5 s at 32 workers. `BatchCan` of 100 items: 61 ms p50, about 5,000 item checks/s. Table below |
| Bounded-scan catalog listing | **Pass.** Page 25, batch 100, scan cap 500. Sparse visibility (alice, 1%) needs 5 `BatchCan` calls per page and returns short pages with a cursor. Dense visibility (bob, 10%) needs 3 calls per full page. No totals are exposed |
| Position grant follows reassignment | **Pass.** The first check after end-and-assign (about 160–200 ms of writes) denies alice and allows bob. Same-day reassignment back works |
| Company isolation | **Fail at one point; Kiban's side only.** Anchoring, non-member checks and context mismatch all hold, but a cross-company position subject is accepted and resolves (below). Isolation in Hareno's queries, cache and exports cannot be tested until they exist; it carries into Phase 2 as tests |
| Disablement-to-access-loss interval | **Measured.** Kiban member deactivation: immediate (next check, about 36 ms). Keycloak disablement: denied on the first check after disablement (`KEYCLOAK_DISABLED`); refresh and new login refused immediately. The access token still passes `VerifyUserToken` until it expires 300 s later |

## Company isolation

| Case | Result |
|---|---|
| Carol (BETA only) asks about an ALPHA report | denied (membership) |
| Dave (both companies) asks about an ALPHA report he may view, in ALPHA | allowed (control) |
| The same report asked in BETA context | denied (anchor) |
| A tuple on an ALPHA report written under BETA | carol still denied |
| A second anchor binding an ALPHA report to BETA | refused (one anchor per object) |
| **An ALPHA report granted to BETA's FIN_LEAD position** | **grant accepted** |
| **Dave holds FIN_LEAD and asks in ALPHA** | **allowed: leak** |

Kiban's `docs/security.md` states that positions and groups are bound to the request's company through their own company. The grant route validates the anchor and the subject's relation name, but not the subject's company, and the engine resolves the userset. Only users who are members of both companies are exposed, because the membership step still applies. Groups were not tested but share the code path.

## Latency

Closed loop, each worker issuing back-to-back calls with tokens from three users:

| Call | Workers | Checks/s | p50 | p95 | p99 |
|---|---|---|---|---|---|
| `Can` | 1 | 29 | 32 ms | 53 ms | 69 ms |
| `Can` | 8 | 81 | 91 ms | 175 ms | 204 ms |
| `Can` | 32 | 86 | 369 ms | 551 ms | 632 ms |
| `Can` | 64 | 95 | 652 ms | 1.03 s | 1.11 s |
| `BatchCan` ×100 | 1 | 1,490 | 64 ms | 92 ms | 112 ms |
| `BatchCan` ×100 | 8 | 4,430 | 171 ms | 289 ms | 393 ms |
| `BatchCan` ×100 | 32 | 4,240 | 763 ms | 1.10 s | 1.16 s |

No errors at any level.

What it means at a five-second refresh:
- One `Can` per viewer per refresh saturates at about 450–500 concurrent viewers, with p95 above 150 ms already at 8 workers.
- With a 30 s decision cache, the same ceiling covers about 2,700 viewers on one open report.
- These numbers come from a contended host, and Kiban's gateway is stateless and replicable (§10.3). They still justify the cache.
- **Why a single check costs about 30 ms:** each decision first evaluates a per-subject prefix (identity lookup, a **live Keycloak admin call** for `kcEnabled`, membership, module state), then the engine.
  - `BatchCan` pays the prefix once per batch: 100 items cost about 62 ms, against about 30 ms for one.
  - So the prefix is about 28 ms and the engine about 0.3 ms per item.
  - Upstream: [kiban-issues/2-disabled-user-token-and-decision-cost.md](kiban-issues/2-disabled-user-token-and-decision-cost.md).

## Decision cache (condition 2)

Authorization decisions only. The query-result cache (§8.2) is separate and is S4's subject.

- **Key:** (company, subject `sub`, feature, object type, object id, relation). The token is not part of the key, but every request still verifies the token and the server-side session before the cache is consulted.
- **Value:** allowed or denied plus Kiban's reason. Allows are kept for **30 s**, denials for **5 s**, so a new share shows up quickly. Both TTLs are settings.
- **Hard expiry.** Nothing is served past its TTL: no stale-while-revalidate, and no serving from cache because Kiban is down. After expiry with Kiban unreachable, the request is denied (`503`); authorization fails closed (§9).
- **Where:** in process, per API replica, as a bounded LRU. Each replica's staleness is bounded by the same TTL, so Valkey adds nothing to correctness. Add it only if replica count makes Kiban load a problem.
- **Fill:** use `BatchCan` whenever a request needs more than one decision, such as a report plus its datasets or a catalog page. Collapse concurrent misses for the same key into one call, which covers several tabs of the same user.
- **Invalidation:** the TTL is the model, because Kiban has no change feed yet (0.3, issue #47). On top of it:
  - When Hareno itself writes grants (its sharing UI, deletion), it drops that object's entries on the local replica at once; other replicas converge within the TTL.
  - At logout or session end, it drops the subject's entries.
- **Bypass:** exports, schedule runs, share-management screens and any write path check Kiban fresh. They are rare and high-value, and staleness there is not acceptable.
- **The promise to the client:** removing access (unsharing, ending a position, deactivating a member, disabling the account) takes effect within 30 s on interactive views and immediately on exports. The client must accept this (§15).
- **Load:** a viewer on a 5 s refresh costs Kiban one decision (or one batch) per 30 s instead of six. At the measured ceiling of about 100 single checks/s, that is roughly 3,000 viewers per Kiban gateway; batching raises it further.

## Other observations

- **Go version.** The SDK module declares `go 1.26.9`, so adopting it raises Hareno's `go.mod` from 1.25.0.
- **HTTP client.** Supply the SDK's `HTTPClient` with a pooled transport. The default `http.Client` keeps only 2 idle connections per host, which churns connections under concurrency.
- **Revoke order, and revival on re-anchor.**
  - Kiban accepts revoking an anchor while other tuples on the object remain.
  - It then refuses to revoke those tuples (`422 … is not anchored`).
  - Re-anchoring the same id silently revives them: alice regained access.
  - So Hareno's deletion path (§10.4 item 5) revokes relations first and the anchor last, in separate calls, and never reuses object ids.
  - A crashed early run of this harness left exactly such stranded tuples in the dev Kiban; they were removed with SQL.
  - Upstream: [kiban-issues/4-revoke-order-orphans.md](kiban-issues/4-revoke-order-orphans.md).
- **One holder per position; no atomic handover.**
  - Assigning while someone holds the position is a bare `409 conflict`.
  - Handover is end then assign. Between the calls both the old and the new holder were denied, for about 200 ms measured.
  - If the second call fails, the position is left empty.
  - Upstream: [kiban-issues/3-position-handover.md](kiban-issues/3-position-handover.md).
- **A disabled user's token still works where no decision runs.** `MeCompanies` with the disabled user's unexpired token still listed both companies; `VerifyUserToken` still succeeds. Covered by condition 3; upstream in issue 2.
- **Member codes are upper-cased** by Kiban (`dave` → `DAVE`); match them case-insensitively.
- **Grant throughput.** 1,111 tuples in batches of 100 took about 0.6 s.
- **Licences.** The SDK pulls in `lestrrat-go/jwx` v3 (MIT) and its MIT/BSD/ISC dependencies, all within the allowlist; `go-licenses` will confirm this when the SDK enters the main module.

## Carried forward

- Phase 2: Hareno's grant validation (condition 1), the decision cache (condition 2), a Kiban decision on every data-returning path and session ending on refused refresh (condition 3), and company-isolation tests over queries, cache and exports.
- Upstream, filed 2026-10-13 and confirmed by Kiban's team:

  | Kiban issue | Subject | Target |
  |---|---|---|
  | #72 | cross-company grant subject (write and check time) | 0.1.3 |
  | #73 | revoke without the anchor; deletion order and id reuse documented | 0.1.3 |
  | #74 | token-only routes serve disabled users | 0.1.3 docs; route check in 0.2 |
  | #75 | the 409 on assign names the current holder | 0.1.3 |
  | #71 | positions on org units (found earlier) | 0.1.3 |
  | #76 | decision cost: cached or event-driven Keycloak state, latency budget | 0.2 |
  | #77 | atomic position handover | 0.2 |

  When 0.1.3 ships, rerun this harness. The cross-company checks and the revoke-order section should flip, and their expectations must be updated with them. Hareno's own safeguards stay in place: subject validation, relations-first deletion, fresh UUIDs, a decision before data.
- Rerun this harness on a dedicated host before sizing production, and when Kiban ships its load harness (0.2, issue #32).

## Running the spike

```sh
dev/kiban/up.sh                       # Kiban stack, seeded and verified
cd spikes/s2 && go run .              # about 7 minutes; -skip-disable skips the 5-minute token wait
```

The harness writes about 1,100 tuples and revokes them at the end. It restores position holders a crashed run may have left reassigned.
