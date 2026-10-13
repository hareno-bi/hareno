# Bug: revoking an anchor strands an object's other tuples, and re-anchoring the same id revives them

**Version:** v0.1.2
**Area:** authz — `POST /api/auth/grants` with `"op":"revoke"`
**Severity:** a stale grant can come back to life, and stranded tuples cannot be removed through the API

## Observed behaviour

1. Revoking an object's `company_module` anchor is accepted **even while other tuples on the object remain**.
2. Revoking any remaining tuple on that object is then **refused**:
   ```
   422 VALIDATION_FAILED: object report:<id> is not anchored to company_module:<companyId>/<app>
   ```
   The revoke path runs the same "object must be anchored" check as grant (`internal/authz/http.go`, `handleGrants`: the `checker.Check(... "company_module" ...)` branch applies to both ops).
3. The stranded tuples stay in `authz.tuple`. The only way to remove them through the API is to re-anchor the object, revoke the tuples, then revoke the anchor again.
4. **Re-anchoring the same object id silently revives every stranded tuple.** A new object that reuses the id inherits the old object's viewers.

## Reproduce

As the app's service client (`hareno-backend`), on app type `report` (`company_module`, `viewer`) in company ALPHA, with user alice a member of ALPHA:

```
# 1. create: anchor + viewer
POST /api/auth/grants {"op":"grant","companyId":"<ALPHA>","tuples":[
  {"objectType":"report","objectId":"r9","relation":"company_module","subjectType":"company_module","subjectId":"<ALPHA>/hareno"},
  {"objectType":"report","objectId":"r9","relation":"viewer","subjectType":"user","subjectId":"<alice sub>"}]}
→ 200;  alice /can on r9 → allowed

# 2. revoke the anchor only
POST /api/auth/grants {"op":"revoke","companyId":"<ALPHA>","tuples":[
  {"objectType":"report","objectId":"r9","relation":"company_module","subjectType":"company_module","subjectId":"<ALPHA>/hareno"}]}
→ 200;  alice /can on r9 → denied (expected)

# 3. revoke the viewer tuple
POST /api/auth/grants {"op":"revoke","companyId":"<ALPHA>","tuples":[
  {"objectType":"report","objectId":"r9","relation":"viewer","subjectType":"user","subjectId":"<alice sub>"}]}
→ 422 VALIDATION_FAILED: object report:r9 is not anchored to company_module:<ALPHA>/hareno

# 4. anchor the same id again (e.g. a new object that reuses the id)
POST /api/auth/grants {"op":"grant", ... anchor tuple only ...}
→ 200;  alice /can on r9 → ALLOWED   ← the old grant is back
```

We also hit this by accident. A cleanup that revoked tuples in batches of 100 put an object's anchor in an earlier batch than its `viewer` tuple. The later batch failed as a whole, which left that batch's tuples (other objects' anchors and relations) in place too.

## Expected

Any of these closes it. Option 1 is the smallest change:

1. **Allow revoke of a relation on an unanchored object.** Removing access never needs the anchor to prove company ownership, as long as `companyId` matches the tuple's (former) anchor or the caller owns the type. The anchor check stays on grant.
2. **Refuse to revoke an anchor while other tuples on the object remain** (`409`, naming the count), so deletion has to go relations-first.
3. **Cascade:** revoking an anchor removes every tuple on the object in the same transaction, which is what deleting an object means.

Separately, please document the deletion order apps must follow and the id-reuse hazard. `docs/integrate.md` step 8 covers creation (anchor first, in one call) but not deletion.

## Impact

- A deleted object's grants can outlive it and return when the id is reused: a new report inherits a deleted report's viewers. Apps that use non-reused UUIDs avoid the revival, but not the stranded rows.
- The cleanup failure can't be fixed from the API. An app hitting the 422 has no supported way out besides "re-anchor, revoke, un-anchor", which nobody will guess.
- Batched revokes fail atomically, so one stranded tuple blocks its whole batch.

## How we found it

Hareno S2 harness, section "revoke order (anchor first)", which reproduces steps 1–4.
