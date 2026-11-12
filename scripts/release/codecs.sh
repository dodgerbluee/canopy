#!/usr/bin/env bash
# Exercise the actual bundled tools without Docker credentials/socket/network.
set -euo pipefail
cd "$(dirname "$0")/../.."
mkdir -p .artifacts/agents/DEP03/bin
architecture=$(docker image inspect canopy-release-test:smoke --format '{{.Architecture}}')
(cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go test -c -o ../.artifacts/agents/DEP03/bin/processor.test ./internal/media/processor && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go test -c -o ../.artifacts/agents/DEP03/bin/insights.test ./internal/insights)
for package in processor insights; do
  if [ "$package" = processor ]; then
    path=server/internal/media/processor
    selection='^TestProcess[A-Z]'
  else
    path=server/internal/insights
    selection='^TestReceiptProcess'
  fi
  docker run --rm --network=none --cap-drop=ALL --security-opt=no-new-privileges \
    --memory=1g --pids-limit=128 --user 10001:10001 -e TMPDIR=/tmp \
    -v "$PWD:/src:ro" -w "/src/$path" canopy-release-test:smoke \
    "/src/.artifacts/agents/DEP03/bin/$package.test" -test.v -test.run "$selection" -test.timeout 5m
done
