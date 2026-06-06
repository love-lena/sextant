#!/usr/bin/env bash
# Run the M2 definition-of-done e2e (tests/e2e/m2-acceptance.md) against the built
# `sextant` binary. Pass -update to regenerate the golden transcript.
#
#   tests/e2e/run.sh            # run the DoD e2e
#   tests/e2e/run.sh -update    # regenerate the golden
set -euo pipefail
cd "$(dirname "$0")/../.."
exec go test -tags e2e -count=1 -v ./tests/e2e/ "$@"
