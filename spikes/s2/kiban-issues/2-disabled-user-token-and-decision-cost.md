# Disabled users: read routes keep serving their token until it expires, and the per-check live Keycloak lookup dominates decision cost

**Version:** v0.1.2 (image quickstart; realm access-token lifetime 300 s)
**Area:** gateway auth (`RequireAuth`), org `/api/org/me/*`, authz decision step 2, Go SDK `VerifyUserToken`
**Type:** bug (part A), performance and documentation (part B)

## What works

When a user is disabled in Keycloak, Kiban's **decision** denies them on the very next check, even with an unexpired access token:

| After disabling dave in Keycloak | Result |
|---|---|
| `POST /api/auth/effective-access/can` with his existing token | denied, `KEYCLOAK_DISABLED`, on the first check (0 s) |
| Refresh-token grant | refused immediately |
| New login | refused immediately |

Deactivating the member in Kiban (`PUT /api/org/admin/members/{id}` with `isActive:false`) is also denied on the next check (about 25 ms).

## Part A: bug — routes without a decision still serve a disabled user

The same disabled user's unexpired token is still accepted wherever the gateway only verifies the JWT and no decision runs:

| Call with the disabled user's token | Result |
|---|---|
| `GET /api/org/me/companies` | `200`, **both companies listed** |
| Go SDK `VerifyUserToken` (local JWT check) | succeeds; it cannot know about disablement |

The user can keep reading whatever those routes expose until the token expires, up to the realm's access-token lifetime (300 s by default). `me/companies` is low value, but the gap is generic: any route behind `RequireAuth` without a decision step behaves this way. That includes the member directory, which lists every member's email (we did not test it after disablement).

**Expected:**
- Either every authenticated gateway route checks the same subject state as decision steps 1–3 (Keycloak enabled, lifecycle active),
- or the docs state plainly that only decisions are revocation-aware and that apps must make a decision before serving data, never rely on a verified token alone. Today `docs/integrate.md` step 6 ("Verify the user's token on every request") can be read as sufficient.

**SDK documentation request:** `VerifyUserToken` documents that it checks signature, issuer, audience and expiry. Please also say that it does **not** reflect disablement, so apps must not treat a verified token as "still allowed".

## Part B: performance — the live Keycloak lookup on every decision

Decision step 2 (`internal/authz/decision/evaluate.go`) asks identity for `kcEnabled`. Identity calls Keycloak's admin API live: "live, never cached, never defaulted to enabled". That is what makes disablement immediate, which is good. But it is paid on every `/can`, and in `/batch-can` once per batch (the subject prefix is evaluated once, then the engine runs per item).

Measured on a shared 8-core host (load average 8–15; treat as pessimistic), closed loop, no errors:

| Call | Workers | Checks/s | p50 | p95 |
|---|---|---|---|---|
| `can` | 1 | 29–32 | 29–32 ms | 49–53 ms |
| `can` | 8 | 81–90 | 78–91 ms | 158–175 ms |
| `can` | 32 | 86–104 | 300–369 ms | 472–551 ms |
| `can` | 64 | 95–113 | 579–652 ms | 736–1,029 ms |
| `batch-can` ×100 items | 1 | 1,490–1,590 | 61–64 ms | 83–92 ms |
| `batch-can` ×100 items | 8 | 4,430–4,940 | 142–171 ms | 279–289 ms |

A single check costs about 30 ms, while 100 items in one batch cost about 62 ms. So roughly 28 ms of each decision is the per-subject prefix (identity lookup, live Keycloak call, membership, module state), and about 0.3 ms per item is the engine. Single checks saturate at about 100/s however many workers are added.

Consequences for apps:
- Every decision depends on Keycloak's admin API being up. When Keycloak is unreachable, every decision fails closed (`DEPENDENCY_UNAVAILABLE`), including for users whose state has not changed.
- Apps with frequent checks (dashboards refreshing every few seconds) must cache decisions themselves to stay within this budget, which reintroduces the staleness the live lookup was meant to remove.

**Requests (any of these):**
1. Cache `kcEnabled` per subject for a short, documented, configurable TTL (for example 5 s, default 0 to keep today's behaviour). Disablement still applies within a bound the operator chooses, and the cost is no longer paid per check.
2. Better: a push path. A Keycloak event listener, or admin-event polling, marks the subject disabled in identity's database. Step 2 then reads local state, and disablement stays immediate without a per-check round trip.
3. Publish the decision latency budget, and add the subject-prefix cost to the planned load harness (issue #32).

## Reproduce

Hareno S2 harness, sections "member deactivation in Kiban", "Keycloak disablement" and "check latency under concurrency". The disablement section disables the user through the Keycloak admin API and polls with the old token until access is lost.
