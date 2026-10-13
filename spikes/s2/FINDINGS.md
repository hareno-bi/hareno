# S2 Kiban integration spike: findings

Date: 2026-10-13. Gate: PROJECT-PLAN.md Phase 1, S2; ARCHITECTURE-PLAN.md §10.4. Kiban v0.1.2 (image quickstart, `dev/kiban`), driven through its Go SDK (`github.com/rosschiu/kiban/sdk` v0.1.2). Harness: [main.go](main.go). Raw output: [results.txt](results.txt).

The host is a shared 8-core machine with a load average of 8–15 from other projects, so the latency figures are a pessimistic bound, not a capacity estimate.

## Recommendation

**Keep Kiban, with one new blocking condition and two design rules.**

1. **Blocking: Kiban lets one company's position grant access to another company's object.**
   - In Hareno: Hareno's authorization module validates every grant's subject before writing. A position or group must belong to the object's company, which Hareno checks by listing that company's positions and groups.
   - Upstream: the issue text is in [kiban-issue-cross-company-subject.md](kiban-issue-cross-company-subject.md).
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
| `Can` | 1 | 32 | 29 ms | 49 ms | 61 ms |
| `Can` | 8 | 90 | 78 ms | 158 ms | 205 ms |
| `Can` | 32 | 104 | 300 ms | 472 ms | 512 ms |
| `Can` | 64 | 113 | 579 ms | 736 ms | 764 ms |
| `BatchCan` ×100 | 1 | 1,590 | 61 ms | 83 ms | 106 ms |
| `BatchCan` ×100 | 8 | 4,940 | 142 ms | 279 ms | 364 ms |
| `BatchCan` ×100 | 32 | 5,180 | 626 ms | 790 ms | 839 ms |

No errors at any level.

What it means at a five-second refresh:
- One `Can` per viewer per refresh saturates at about 450–500 concurrent viewers, with p95 above 150 ms already at 8 workers.
- With a 30 s decision cache, the same ceiling covers about 2,700 viewers on one open report.
- These numbers come from a contended host, and Kiban's gateway is stateless and replicable (§10.3). They still justify the cache: a check costs Hareno a network round trip through the gateway, identity, org, registry and authz.

## Other observations

- **Go version.** The SDK module declares `go 1.26.9`, so adopting it raises Hareno's `go.mod` from 1.25.0.
- **HTTP client.** Supply the SDK's `HTTPClient` with a pooled transport. The default `http.Client` keeps only 2 idle connections per host, which churns connections under concurrency.
- **Revoke order.** Revoke relations before the anchor. Kiban refuses to revoke a relation on an object whose anchor is already gone (`422 … is not anchored`). Hareno's deletion path (§10.4 item 5) must revoke relations first and the anchor last.
- **One holder per position.**
  - A second assignment is a `409`.
  - Reassignment takes two calls, end then assign, and is not atomic: for about 150 ms nobody holds the position.
  - Assigning first is refused.
- **Member codes are upper-cased** by Kiban (`dave` → `DAVE`); match them case-insensitively.
- **Grant throughput.** 1,111 tuples in batches of 100 took about 0.6 s.
- **Licences.** The SDK pulls in `lestrrat-go/jwx` v3 (MIT) and its MIT/BSD/ISC dependencies, all within the allowlist; `go-licenses` will confirm this when the SDK enters the main module.

## Carried forward

- Phase 2: Hareno's grant validation (condition 1), the decision cache (condition 2), a Kiban decision on every data-returning path and session ending on refused refresh (condition 3), and company-isolation tests over queries, cache and exports.
- Upstream: the cross-company subject issue, to be filed by the owner.
- Rerun this harness on a dedicated host before sizing production, and when Kiban ships its load harness (0.2, issue #32).

## Running the spike

```sh
dev/kiban/up.sh                       # Kiban stack, seeded and verified
cd spikes/s2 && go run .              # about 7 minutes; -skip-disable skips the 5-minute token wait
```

The harness writes about 1,100 tuples and revokes them at the end. It restores position holders a crashed run may have left reassigned.
