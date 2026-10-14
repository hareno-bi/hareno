#!/bin/sh
# Seed Hareno's dev Kiban, then verify it. Seeding runs once (skipped when alice already
# belongs to a company); verification runs every time. Requires curl, jq, openssl.
#
# Seeded (dev-only password for every user: hareno-dev-password):
#   ALPHA "Alpha Trading Ltd": business units NORTH, SOUTH, WEST (match the dev mart regions)
#     members alice, bob, dave, erin; position NORTH_MGR held by alice; group FINANCE {bob, dave}
#     (erin has no data access of her own: she sees only what is shared with her)
#   BETA "Beta Logistics Ltd": business unit HQ
#     members carol, dave; position FIN_LEAD held by carol
#   Kiban 0.1.2 places every position at the company root: the admin create route rejects
#   orgUnitId and the gateway has no position update route.
#   App "hareno" registered and enabled, service client hareno-backend
#     (its secret is written to dev/kiban/hareno-backend-secret).
set -eu
cd "$(dirname "$0")"
. ./.env
GW=http://localhost:$KIBAN_GATEWAY_HTTP_HOST_PORT
KC=http://localhost:$KEYCLOAK_HOST_PORT
KCA=$KC/admin/realms/$KEYCLOAK_REALM
DEV_PW=hareno-dev-password
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

kc_admin=$(curl -sf "$KC/realms/master/protocol/openid-connect/token" -d grant_type=password -d client_id=admin-cli \
    --data-urlencode "username=$KC_BOOTSTRAP_ADMIN_USERNAME" --data-urlencode "password=$KC_BOOTSTRAP_ADMIN_PASSWORD" |
    jq -r .access_token)
# kc ARGS...: call the Keycloak admin API; on failure, report the response on stderr.
kc() {
    out=$(curl -s --fail-with-body -H "Authorization: Bearer $kc_admin" -H 'content-type: application/json' "$@") ||
        { echo "keycloak error: $* -> $out" >&2; return 1; }
    echo "$out"
}
kc_user_id() { kc "$KCA/users?username=$1&exact=true" | jq -r '.[0].id // empty'; }

# ensure_user NAME PASSWORD: create the realm user if absent; set a permanent password.
ensure_user() {
    id=$(kc_user_id "$1")
    if [ -z "$id" ]; then
        kc "$KCA/users" -d "$(jq -n --arg u "$1" \
            '{username: $u, email: ($u + "@example.invalid"), firstName: $u, lastName: "Dev",
              enabled: true, emailVerified: true, requiredActions: []}')"
        id=$(kc_user_id "$1")
    else
        kc -X PUT "$KCA/users/$id" -d '{"requiredActions": [], "enabled": true}'
    fi
    kc -X PUT "$KCA/users/$id/reset-password" -d "$(jq -n --arg p "$2" '{type: "password", value: $p, temporary: false}')"
}

# login NAME PASSWORD: scripted PKCE authorization-code login through the gateway; prints an access token.
login() {
    jar=$TMP/jar.$1
    rm -f "$jar"  # a leftover SSO session would skip the login form
    redirect=http://localhost:5173/callback
    verifier=$(openssl rand -base64 48 | tr '+/' '-_' | tr -d '=\n')
    challenge=$(printf %s "$verifier" | openssl dgst -sha256 -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')
    action=$(curl -sf -c "$jar" -b "$jar" -G "$GW/auth/realms/$KEYCLOAK_REALM/protocol/openid-connect/auth" \
        -d client_id=kiban-frontend --data-urlencode "redirect_uri=$redirect" -d response_type=code -d scope=openid \
        -d "code_challenge=$challenge" -d code_challenge_method=S256 -d state=dev |
        tr '\n' ' ' | sed -n 's/.*id="kc-form-login"[^>]*action="\([^"]*\)".*/\1/p' | sed 's/&amp;/\&/g')
    code=$(curl -s -o /dev/null -w '%{redirect_url}' -c "$jar" -b "$jar" "$action" \
        --data-urlencode "username=$1" --data-urlencode "password=$2" -d credentialId= |
        sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
    [ -n "$code" ] || { echo "login failed for $1" >&2; exit 1; }
    curl -sf "$GW/auth/realms/$KEYCLOAK_REALM/protocol/openid-connect/token" -d grant_type=authorization_code \
        -d "code=$code" --data-urlencode "redirect_uri=$redirect" -d client_id=kiban-frontend -d "code_verifier=$verifier" |
        jq -r .access_token
}

# api TOKEN ARGS...: call the gateway; prints the body, or reports it on stderr on failure.
api() {
    t=$1; shift
    out=$(curl -s --fail-with-body -H "Authorization: Bearer $t" -H 'content-type: application/json' "$@") ||
        { echo "api error: $* -> $out" >&2; return 1; }
    echo "$out"
}
# api_id TOKEN ARGS...: like api, prints only .data.id.
api_id() { r=$(api "$@"); echo "$r" | jq -r .data.id; }
# my_companies USER: the user's company codes, comma-separated. Fails loudly (no pipeline masking).
my_companies() {
    t=$(login "$1" "$DEV_PW")
    r=$(api "$t" "$GW/api/org/me/companies")
    echo "$r" | jq -r '[.data[].code] | sort | join(",")'
}

SA_PW=$(cat secrets-in/superadmin-password)
ensure_user "$KIBAN_SUPERADMIN_USERNAME" "$SA_PW"
for u in alice bob carol dave erin; do ensure_user "$u" "$DEV_PW"; done

# register_app TOKEN: register (or re-register) the hareno app and its object types.
#   report: viewer/editor. connection: sql_user may use the SQL editor on it; data_sharer may
#     share reports on it showing their own data to people who could not see it.
#   data_scope: viewer holds that row scope, e.g. mart.region.North or mart.region.* (all).
register_app() {
    api "$1" "$GW/api/platform/admin/apps" -d '{
        "key": "hareno", "displayName": "Hareno", "version": "0.0.3", "serviceClientId": "hareno-backend",
        "features": ["hareno.use"],
        "authzFragment": {
            "report": {"company_module": {"this": true}, "viewer": {"this": true}, "editor": {"this": true}},
            "connection": {"company_module": {"this": true}, "sql_user": {"this": true}, "data_sharer": {"this": true}},
            "data_scope": {"company_module": {"this": true}, "viewer": {"this": true}}
        }
    }' >/dev/null
}

seeded=$(my_companies alice)
if [ -n "$seeded" ]; then
    echo "already seeded"
else
    echo "seeding"
    sa=$(login "$KIBAN_SUPERADMIN_USERNAME" "$SA_PW")

    register_app "$sa"
    api "$sa" -X POST "$GW/api/platform/admin/modules/hareno/enable" >/dev/null

    unit() { api_id "$sa" "$GW/api/org/admin/units" -d "$(jq -n --arg t "$1" --arg p "$2" --arg c "$3" --arg n "$4" \
        '{typeKey: $t, parentId: (if $p == "" then null else $p end), code: $c, name: $n}')"; }
    alpha=$(unit company "" ALPHA "Alpha Trading Ltd")
    beta=$(unit company "" BETA "Beta Logistics Ltd")
    unit business_unit "$alpha" NORTH North >/dev/null
    unit business_unit "$alpha" SOUTH South >/dev/null
    unit business_unit "$alpha" WEST West >/dev/null
    unit business_unit "$beta" HQ "Head Office" >/dev/null

    # Kiban creates the identity record on a user's first gateway request; link-user needs it.
    for u in alice bob carol dave; do x=$(my_companies "$u"); done

    # member COMPANY USER: create the member, link it to the user's login; prints the member id.
    member() {
        id=$(api_id "$sa" "$GW/api/org/admin/members" -d "$(jq -n --arg c "$1" --arg u "$2" \
            '{companyId: $c, code: $u, displayName: $u, email: ($u + "@example.invalid")}')")
        api "$sa" "$GW/api/org/admin/members/$id/link-user" -d "$(jq -n --arg s "$(kc_user_id "$2")" '{kcSub: $s}')" >/dev/null
        echo "$id"
    }
    m_alice=$(member "$alpha" alice)
    m_bob=$(member "$alpha" bob)
    m_dave_a=$(member "$alpha" dave)
    m_carol=$(member "$beta" carol)
    member "$beta" dave >/dev/null

    position() { api_id "$sa" "$GW/api/org/admin/companies/$1/positions" -d "$(jq -n --arg c "$2" --arg t "$3" \
        '{code: $c, title: $t}')"; }
    assign() { api "$sa" "$GW/api/org/admin/positions/$1/assignments" -d "{\"memberId\": \"$2\"}" >/dev/null; }
    p=$(position "$alpha" NORTH_MGR "North Regional Manager"); assign "$p" "$m_alice"
    p=$(position "$beta" FIN_LEAD "Finance Lead"); assign "$p" "$m_carol"

    finance=$(api_id "$sa" "$GW/api/org/admin/companies/$alpha/groups" -d '{"code": "FINANCE", "name": "Finance"}')
    for m in "$m_bob" "$m_dave_a"; do api "$sa" "$GW/api/org/admin/groups/$finance/members" -d "{\"memberId\": \"$m\"}" >/dev/null; done
fi

# erin (added after the first seed): an ALPHA member with no grants.
if [ -z "$(my_companies erin)" ]; then
    sa=$(login "$KIBAN_SUPERADMIN_USERNAME" "$SA_PW")
    alpha=$(api "$(login alice "$DEV_PW")" "$GW/api/org/me/companies" | jq -r '.data[] | select(.code == "ALPHA") | .id')
    id=$(api_id "$sa" "$GW/api/org/admin/members" -d "$(jq -n --arg c "$alpha" '{companyId: $c, code: "erin", displayName: "erin", email: "erin@example.invalid"}')")
    api "$sa" "$GW/api/org/admin/members/$id/link-user" -d "$(jq -n --arg s "$(kc_user_id erin)" '{kcSub: $s}')" >/dev/null
fi

# Service client secret for Hareno's backend.
client=$(kc "$KCA/clients?clientId=hareno-backend" | jq -r '.[0].id')
kc "$KCA/clients/$client/client-secret" | jq -r .value > hareno-backend-secret
chmod 600 hareno-backend-secret

# Hareno's dev grants, every run (grants are idempotent): the app's object types are current,
# and ALPHA's mart connection is usable with SQL by the North manager (North only) and the
# Finance group (all regions). The North manager may share her data. BETA has no access to the mart.
sa=$(login "$KIBAN_SUPERADMIN_USERNAME" "$SA_PW")
register_app "$sa"
svc=$(curl -sf "$GW/auth/realms/$KEYCLOAK_REALM/protocol/openid-connect/token" -d grant_type=client_credentials \
    -d client_id=hareno-backend --data-urlencode "client_secret=$(cat hareno-backend-secret)" | jq -r .access_token)
alpha=$(api "$(login alice "$DEV_PW")" "$GW/api/org/me/companies" | jq -r '.data[] | select(.code == "ALPHA") | .id')
north_mgr=$(api "$sa" "$GW/api/org/admin/companies/$alpha/positions" | jq -r '.data.items[] | select(.code == "NORTH_MGR") | .id')
finance=$(api "$sa" "$GW/api/org/admin/companies/$alpha/groups" | jq -r '.data.items[] | select(.code == "FINANCE") | .id')
api "$svc" "$GW/api/auth/grants" -d "$(jq -n --arg c "$alpha" --arg p "$north_mgr" --arg f "$finance" '
    def anchor($t; $id): {objectType: $t, objectId: $id, relation: "company_module", subjectType: "company_module", subjectId: ($c + "/hareno")};
    def to_pos($t; $id; $r): {objectType: $t, objectId: $id, relation: $r, subjectType: "position", subjectId: $p, subjectRelation: "holder"};
    def to_fin($t; $id; $r): {objectType: $t, objectId: $id, relation: $r, subjectType: "group", subjectId: $f, subjectRelation: "member"};
    {op: "grant", companyId: $c, tuples: [
        anchor("connection"; "mart"), to_pos("connection"; "mart"; "sql_user"), to_fin("connection"; "mart"; "sql_user"),
        to_pos("connection"; "mart"; "data_sharer"),
        anchor("data_scope"; "mart.region.North"), to_pos("data_scope"; "mart.region.North"; "viewer"),
        anchor("data_scope"; "mart.region.*"), to_fin("data_scope"; "mart.region.*"; "viewer")]}')" >/dev/null

# Verify: company membership per user, and the service client sees the enabled app.
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok  $1"; else echo "FAIL $1: expected '$3', got '$2'"; fail=1; fi; }
check "alice companies" "$(my_companies alice)" ALPHA
check "bob companies"   "$(my_companies bob)"   ALPHA
check "carol companies" "$(my_companies carol)" BETA
check "dave companies"  "$(my_companies dave)"  ALPHA,BETA
check "erin companies"  "$(my_companies erin)"  ALPHA
sa=$(login "$KIBAN_SUPERADMIN_USERNAME" "$SA_PW")
company_id() { r=$(api "$(login "$1" "$DEV_PW")" "$GW/api/org/me/companies"); echo "$r" | jq -r --arg c "$2" '.data[] | select(.code == $c) | .id'; }
holders() { r=$(api "$sa" "$GW/api/org/admin/companies/$1/positions"); echo "$r" | jq -r '[.data.items[] | "\(.code)=\(.holderDisplayName)"] | sort | join(",")'; }
alpha=$(company_id alice ALPHA)
beta=$(company_id carol BETA)
check "ALPHA position holders" "$(holders "$alpha")" NORTH_MGR=alice
check "BETA position holders"  "$(holders "$beta")"  FIN_LEAD=carol
r=$(api "$sa" "$GW/api/org/admin/companies/$alpha/groups")
finance=$(echo "$r" | jq -r '.data.items[] | select(.code == "FINANCE") | .id')
r=$(api "$sa" "$GW/api/org/admin/groups/$finance/members")
check "ALPHA FINANCE members" "$(echo "$r" | jq -r '[.data[].memberDisplayName] | sort | join(",")')" bob,dave
svc=$(curl -sf "$GW/auth/realms/$KEYCLOAK_REALM/protocol/openid-connect/token" -d grant_type=client_credentials \
    -d client_id=hareno-backend --data-urlencode "client_secret=$(cat hareno-backend-secret)" | jq -r .access_token)
check "hareno app enabled (service token)" "$(api "$svc" "$GW/api/platform/capabilities/hareno" | jq -r .data.enabled)" true
# allowed USER OBJECT_TYPE OBJECT_ID RELATION: prints true or false for USER in their first company.
allowed() {
    t=$(login "$1" "$DEV_PW")
    c=$(api "$t" "$GW/api/org/me/companies" | jq -r '.data[0].id')
    api "$t" "$GW/api/auth/effective-access/can" -d "$(jq -n --arg c "$c" --arg t "$2" --arg i "$3" --arg r "$4" \
        '{featureKey: "hareno.use", moduleKey: "hareno", companyId: $c, object: {type: $t, id: $i}, relation: $r}')" | jq -r .data.allowed
}
check "alice sql on mart"     "$(allowed alice connection mart sql_user)" true
check "alice scope North"     "$(allowed alice data_scope mart.region.North viewer)" true
check "alice scope all"       "$(allowed alice data_scope 'mart.region.*' viewer)" false
check "bob scope all"         "$(allowed bob data_scope 'mart.region.*' viewer)" true
check "carol sql on mart"     "$(allowed carol connection mart sql_user)" false
check "alice shares her data" "$(allowed alice connection mart data_sharer)" true
check "bob shares his data"   "$(allowed bob connection mart data_sharer)" false
check "erin sql on mart"      "$(allowed erin connection mart sql_user)" false
exit $fail
