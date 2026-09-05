#!/bin/sh
# A selected acceptance test must pass, not silently skip or match zero tests.
set -eu
if [ "$#" -lt 2 ]; then
  echo "usage: $0 TestName package [go test flags...]" >&2
  exit 2
fi
required_test=$1
package=$2
shift 2
result=$(mktemp)
trap 'rm -f "$result"' EXIT HUP INT TERM
if ! go test -json -tags=integration -count=1 -run "^${required_test}$" "$package" "$@" > "$result"; then
  cat "$result"
  exit 1
fi
python3 - "$result" "$required_test" <<'PYTHON'
import json
import sys

passed = False
with open(sys.argv[1]) as events:
    for line in events:
        event = json.loads(line)
        if event.get("Output"):
            print(event["Output"], end="")
        if event.get("Test") == sys.argv[2] and event["Action"] == "pass":
            passed = True
if not passed:
    sys.exit(f"NOT ACCEPTED: {sys.argv[2]} did not execute and pass")
PYTHON
