#!/bin/bash
# Fails when any manifest disagrees with VERSION, the single source of the product version.
#   scripts/check-version.sh
set -euo pipefail
cd "$(dirname "$0")/.."
want=$(tr -d '[:space:]' < VERSION)
status=0
for manifest in package.json core/package.json shared/package.json web/package.json sdk/package.json cli/package.json; do
  [ -f "$manifest" ] || continue
  have=$(node -p "require('./$manifest').version")
  if [ "$have" != "$want" ]; then echo "$manifest: version $have, VERSION says $want"; status=1; fi
done
if [ -f agent/cmd/hoplet-agent/main.go ]; then
  have=$(sed -n 's/^const version = "\([^"]*\)".*$/\1/p' agent/cmd/hoplet-agent/main.go)
  if [ "$have" != "$want" ]; then echo "agent/cmd/hoplet-agent/main.go: version $have, VERSION says $want"; status=1; fi
fi
[ $status -eq 0 ] && echo "version $want everywhere"
exit $status
