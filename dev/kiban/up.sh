#!/bin/sh
# Start Hareno's dev Kiban as its own compose project, then seed and verify it.
# First run generates dev/kiban/.env and the superadmin password (both gitignored).
set -eu
cd "$(dirname "$0")"
if [ ! -f .env ]; then
    cp .env.example .env
    for k in POSTGRES_PASSWORD KIBAN_DB_PASSWORD KC_DB_PASSWORD KC_BOOTSTRAP_ADMIN_PASSWORD KEYCLOAK_IDENTITY_CLIENT_SECRET \
             KIBAN_REGISTRY_DB_PASSWORD KIBAN_IDENTITY_DB_PASSWORD KIBAN_ORG_DB_PASSWORD \
             KIBAN_AUTHZ_DB_PASSWORD KIBAN_NOTIFICATION_DB_PASSWORD KIBAN_TIMESHEET_DB_PASSWORD \
             KIBAN_DOCS_DB_PASSWORD KIBAN_HELPDESK_DB_PASSWORD KIBAN_NOTIFICATION_WEBHOOK_HMAC_SECRET; do
        sed -i "s|^${k}=\$|${k}=$(openssl rand -hex 24)|" .env
    done
    # Hareno's 554xx range; the issuer URL follows the gateway port.
    sed -i 's|^POSTGRES_HOST_PORT=.*|POSTGRES_HOST_PORT=55442|
            s|^KEYCLOAK_HOST_PORT=.*|KEYCLOAK_HOST_PORT=55441|
            s|^KIBAN_GATEWAY_HTTP_HOST_PORT=.*|KIBAN_GATEWAY_HTTP_HOST_PORT=55440|' .env
    printf 'KIBAN_SERVICE_CLIENTS=hareno-backend\nKIBAN_EXTRA_ORIGINS=http://localhost:55440\n' >> .env
    mkdir -p secrets-in
    openssl rand -base64 24 > secrets-in/superadmin-password
    chmod 600 secrets-in/superadmin-password
fi
docker compose -p hareno-kiban up -d --wait
./seed.sh
