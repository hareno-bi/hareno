# Grant accepts a position from another company as subject; the check then allows it

**Version:** v0.1.2 (image quickstart).

**Documented rule (docs/security.md, Authorization):** "Every object check is bound to the request's company: companies and company modules by id, positions and groups through their company, module objects through the anchor tuple their module writes at creation."

**Observed:** an app writes, under company A, a relation on its own A-anchored object whose subject is a position of company B. `POST /api/auth/grants` accepts it. A user who is a member of both companies and holds the B position is then **allowed** on the A object, with the request in company A's context.

## Reproduce

1. Companies ALPHA and BETA. User dave is a member of both. Position FIN_LEAD is in BETA.
2. App `hareno` (type `report`: `company_module`, `viewer`) registered and enabled.
3. As the app's service client:
   ```
   POST /api/auth/grants
   {"op":"grant","companyId":"<ALPHA>","tuples":[
     {"objectType":"report","objectId":"r1","relation":"company_module","subjectType":"company_module","subjectId":"<ALPHA>/hareno"},
     {"objectType":"report","objectId":"r1","relation":"viewer","subjectType":"position","subjectId":"<FIN_LEAD id>","subjectRelation":"holder"}]}
   ```
   → 200 (expected 422: the subject's company is not ALPHA).
4. Assign FIN_LEAD to dave's BETA member.
5. With dave's bearer:
   ```
   POST /api/auth/effective-access/can
   {"featureKey":"hareno.use","moduleKey":"hareno","companyId":"<ALPHA>","object":{"type":"report","id":"r1"},"relation":"viewer"}
   ```
   → `allowed: true`. Expected: denied.

## Expected

- Either the grant is refused (`422`) when a `position` or `group` subject belongs to a company other than `companyId`,
- or the check ignores a userset subject from another company,
- preferably both, matching the anchor rule's "unique index on every write path".

Groups are presumably affected the same way (not tested).

## Impact

Any app bug, or any caller that lets a user pick a position or group by id, can share one company's objects with holders in another company. Only users who are members of both companies are exposed, because the membership step still applies.

Found by Hareno's S2 spike (`spikes/s2/main.go`, section "company isolation").
