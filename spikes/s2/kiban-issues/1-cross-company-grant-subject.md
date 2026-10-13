# Security: a grant accepts another company's position as subject, and the check then allows access across companies

**Version:** v0.1.2 (image quickstart, `deploy/quickstart`)
**Area:** authz — `POST /api/auth/grants`, `POST /api/auth/effective-access/can`
**Severity:** security. It breaks the company boundary the docs promise.

## Documented behaviour

`docs/security.md`, *Authorization*:

> Every object check is bound to the request's company: companies and company modules by id, positions and groups through their company, module objects through the anchor tuple their module writes at creation. An object from another company is denied.

## Observed behaviour

- An app writes, in company ALPHA, a `viewer` tuple on one of its own ALPHA-anchored objects, with a position from company BETA as subject. `POST /api/auth/grants` accepts it with `200`.
- A user who is a member of both companies and holds that BETA position is then **allowed** on the ALPHA object, with the request in ALPHA's context.

The grant handler (`internal/authz/http.go`, `handleGrants`) validates the object's anchor and checks that `subjectRelation` exists on the subject type. Nothing checks which company the subject belongs to. The engine then resolves `position:<BETA position>#holder` → `member:<BETA member>#mapped_user` → `user:<sub>`, the same user who passed the ALPHA membership step.

## Reproduce

Setup:
- Companies ALPHA and BETA.
- User **dave** is a member of both (one member record in each, both linked to his login).
- Position **FIN_LEAD** exists in BETA.
- App `hareno` is registered and enabled. Its manifest declares type `report` with relations `company_module` (`this`) and `viewer` (`this`); its service client is `hareno-backend`.

1. With the app's service token, anchor an object to ALPHA and grant `viewer` to BETA's position:
   ```
   POST /api/auth/grants
   Authorization: Bearer <hareno-backend service token>
   {"op":"grant","companyId":"<ALPHA id>","tuples":[
     {"objectType":"report","objectId":"r1","relation":"company_module",
      "subjectType":"company_module","subjectId":"<ALPHA id>/hareno"},
     {"objectType":"report","objectId":"r1","relation":"viewer",
      "subjectType":"position","subjectId":"<FIN_LEAD id>","subjectRelation":"holder"}]}
   ```
   **Actual:** `200`. **Expected:** `422`, because FIN_LEAD belongs to BETA, not ALPHA.

2. As superadmin, make dave's BETA member the holder of FIN_LEAD (ending the current holder's assignment first, since a position has one holder):
   ```
   POST /api/org/admin/assignments/<current FIN_LEAD assignment id>/end
   POST /api/org/admin/positions/<FIN_LEAD id>/assignments   {"memberId":"<dave's BETA member id>"}
   ```

3. With dave's own bearer, ask in ALPHA's context:
   ```
   POST /api/auth/effective-access/can
   {"featureKey":"hareno.use","moduleKey":"hareno","companyId":"<ALPHA id>",
    "object":{"type":"report","id":"r1"},"relation":"viewer"}
   ```
   **Actual:** `{"allowed":true,"reason":"ALLOWED"}`. **Expected:** denied.

## Controls (these behave correctly)

- Carol, a BETA-only member, asking about an ALPHA object in ALPHA: denied (membership).
- Dave asking about an ALPHA object in BETA's context: denied (anchor).
- A second anchor binding an ALPHA object to BETA: refused with `422`.

## Expected fix

Two layers, as for anchors ("a unique index on the tuple table holds that rule for every write path"):

1. **Write time:** refuse a grant whose `position`, `group` or `member` subject belongs to a company other than `companyId`. Return `422 VALIDATION_FAILED` naming the tuple.
2. **Check time:** when the engine expands a userset subject (`position#holder`, `group#member`), ignore any subject whose company is not the request's company. This also covers tuples written before the write-time fix.

## Not tested

- Groups (`group:<BETA group>#member`), which presumably follow the same code path.
- Direct `member:` subjects from another company.

## Impact

- Any app bug can share one company's objects with people in another company, and so can any app UI that lets a user pick a position or group by id.
- Only users who are members of both companies get through, because the membership step still applies. Those users are typically the most senior (group finance, shared services).
- The leak is silent: the check says `ALLOWED`, and nothing in the audit marks the tuple as cross-company.

## How we found it

The Hareno S2 integration spike, section "company isolation". It runs as a Go program against the image quickstart and reproduces in about 30 s.
