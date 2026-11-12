#!/usr/bin/env bash
# T32.a e2e harness: Playwright runs in the pinned container while a host
# worker processes media jobs — the processor needs the docker sandbox, which
# the Playwright container has no daemon access to. The API (inside the
# container) and the worker (on the host) share one disposable ObjectStore
# directory (.artifacts/e2e/storage) and the same disposable schema.
# global-setup.mjs writes e2e-env.json once the schema exists; this script
# then starts the worker and lets the suite run.
#
# T34 adds a stub AI provider (web/e2e/stub-provider.mjs) in its own
# container with a published port: the host worker reaches it at 127.0.0.1,
# the in-container API reaches it at host.docker.internal (Docker Desktop).
# global-setup.mjs forwards E2E_AI_BASE_URL into the API process env.
set -uo pipefail
cd "$(dirname "$0")/.."
ENVFILE=.artifacts/e2e/e2e-env.json
rm -f "$ENVFILE"

STUB_PORT=${STUB_PORT:-8098}
STUB_NAME=canopy-e2e-stub
export E2E_AI_BASE_URL="http://host.docker.internal:${STUB_PORT}"
# cap covers the capture journey's receipt-structuring run plus the
# insights journey's cancel/rerun/budget sequence; insights discovers the
# exact remaining count by clicking, so this only needs to be generous
export E2E_AI_DAILY_RUN_LIMIT="${E2E_AI_DAILY_RUN_LIMIT:-4}"

docker rm -f "$STUB_NAME" 2>/dev/null >/dev/null
docker run -d --rm --name "$STUB_NAME" -p "${STUB_PORT}:8098" \
  -v "$PWD":/w -w /w -e STUB_PORT=8098 -e STUB_DELAY_MS="${STUB_DELAY_MS:-4000}" \
  node:24 node web/e2e/stub-provider.mjs >/dev/null

docker run --rm -v "$PWD":/w -w /w \
  -e E2E_AI_BASE_URL -e E2E_AI_DAILY_RUN_LIMIT \
  "${PW_IMAGE:?PW_IMAGE must be set}" \
  sh -c "cd /w/web && npx ng build --configuration development && npx playwright test --config playwright.e2e.config.ts ${E2E_ARGS:-}" &
pw=$!

worker=''
for _ in $(seq 1 240); do
  [ -s "$ENVFILE" ] && break
  kill -0 "$pw" 2>/dev/null || break
  sleep 0.5
done
if [ -s "$ENVFILE" ]; then
  dsn=$(python3 -c "import json;print(json.load(open('$ENVFILE'))['dsn'])")
  DATABASE_URL="$dsn" CANOPY_ENV=test CANOPY_LOG_LEVEL="${E2E_WORKER_LOG:-warn}" \
    CANOPY_STORAGE_ROOT="$PWD/.artifacts/e2e/storage" \
    CANOPY_AI_BASE_URL="http://127.0.0.1:${STUB_PORT}" CANOPY_AI_API_KEY=e2e-stub-key \
    CANOPY_AI_MODEL=e2e-stub CANOPY_AI_DAILY_RUN_LIMIT="$E2E_AI_DAILY_RUN_LIMIT" \
    ./.artifacts/e2e/worker-host > .artifacts/e2e/worker.log 2>&1 &
  worker=$!
else
  echo "e2e_with_worker: env file never appeared; running without a media worker" >&2
fi

wait "$pw"; rc=$?
[ -n "$worker" ] && kill "$worker" 2>/dev/null
docker logs "$STUB_NAME" > .artifacts/e2e/stub.log 2>&1
docker stop "$STUB_NAME" 2>/dev/null >/dev/null
exit "$rc"
