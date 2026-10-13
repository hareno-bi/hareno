# Feature: atomic position handover (end and assign in one transaction)

**Version:** v0.1.2
**Area:** org — `POST /api/org/admin/assignments/{id}/end`, `POST /api/org/admin/positions/{id}/assignments`
**Type:** feature request, with a correctness gap

## Current behaviour

A position has one holder at a time. Handing it from one member to another takes two calls, in this order:

1. `POST /api/org/admin/assignments/<current assignment id>/end`: revokes `position:<id>#holder@member:<old>#mapped_user`.
2. `POST /api/org/admin/positions/<id>/assignments {"memberId":"<new>"}`: grants the holder tuple to the new member.

Assigning first is refused while someone holds the position:
```
POST /api/org/admin/positions/<id>/assignments {"memberId":"<new>"}
→ 409 {"error":{"code":"CONFLICT","message":"conflict"}}
```

## Problems

1. **A window where nobody holds the position.** Between the two calls, every grant to `position:<id>#holder` answers denied for everyone. Measured on the image quickstart: about 200 ms between the end returning and the assign returning, during which checks for both the old and the new holder were denied. Over a slow link, or if the second call fails, the window is unbounded.
2. **A half-done handover on failure.** If step 2 fails (network, a validation error such as an inactive member, the gateway restarting), the position is left empty. The caller must notice and retry or roll back. Nothing records that a handover was in progress.
3. **The 409 says nothing useful.** The body is `"conflict"`, with no detail naming the current holder or assignment id, or saying "end the current assignment first".

## Request

- A single handover operation that ends the current assignment and creates the new one in one transaction, with one audit event (or two linked ones). For example:
  ```
  POST /api/org/admin/positions/{id}:handover
  {"memberId":"<new member id>"}
  → 200 {"ended": <old assignment>, "assignment": <new assignment>}
  ```
  It refuses when the position is empty (use assign), or the new member is inactive or in another company. It is idempotent when the new member already holds the position.
- Optionally an `expectedAssignmentId` field, so concurrent admins cannot hand over twice.
- A 409 detail for plain assign, e.g. `{"code":"POSITION_HELD","details":{"assignmentId":"…","memberId":"…"}}`.
- The Go, Node and Python SDKs expose it. Today the SDK has `PositionHolder` but no admin write calls, so apps call the REST routes directly.

## Why it matters to apps

Position-based grants are the main reason to use positions: access follows the chair. A handover is the moment that promise is tested. Apps polling every few seconds (dashboards) show the outgoing and incoming holder an "access denied" state during the window. If the second call fails, both lose access until someone notices.

## Reproduce

Hareno S2 harness, section "position reassignment". It checks the 409 on assign-first, the denied/denied state between the calls, and the window length.
