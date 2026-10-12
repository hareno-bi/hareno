#!/bin/sh
# Run the known-answer checks against the dev mart (starts it if needed).
set -e
cd "$(dirname "$0")/../.."
docker compose up -d --wait mart
docker compose exec -T mart psql -U postgres -d mart -q < dev/mart/known-answers.sql
