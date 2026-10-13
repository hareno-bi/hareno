#!/bin/sh
# Write the S3 sample data (public/s3/*.json) from the dev mart. The files are generated, not committed.
set -e
cd "$(dirname "$0")/.."
mkdir -p public/s3
out=$(docker compose -f ../compose.yaml exec -T mart psql -U postgres -d mart -qAt < scripts/s3-snapshot.sql)
echo "$out" | sed -n 1p > public/s3/charts.json
echo "$out" | sed -n 2p > public/s3/matrix.json
wc -c public/s3/*.json
