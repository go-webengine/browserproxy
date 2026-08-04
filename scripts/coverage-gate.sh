#!/usr/bin/env bash
# Copyright (c) the go-webengine/browserproxy authors.
# SPDX-License-Identifier: BSD-3-Clause
#
# Coverage gate for the root package's pure logic: the protocol codec, the SSRF
# guard, session state / scroll slicing / history, and the WebSocket dispatch
# and read→write loop. All of it is deterministic and font-free, driven by an
# injected fake renderer, so it is fully unit-testable offline and held at 100%.
#
# cmd/browserproxy is excluded: main() and the signal/ListenAndServe wiring are
# thin process-lifecycle glue whose remaining branches (an immediate
# ListenAndServe success, a Shutdown deadline) are not deterministically
# reachable in a unit test.
set -euo pipefail

FLOOR=100.0

out=$(CGO_ENABLED=0 go test -short -covermode=count -coverprofile=/tmp/browserproxy.cov . 2>&1)
echo "$out"
cov=$(printf '%s\n' "$out" | grep -oE 'coverage: [0-9.]+% of statements' | grep -oE '[0-9.]+' | head -1)
if [ -z "${cov:-}" ]; then
  echo "::error::could not parse coverage"
  exit 1
fi
if awk -v c="$cov" -v f="$FLOOR" 'BEGIN{exit !(c+0 >= f-0)}'; then
  echo "OK  root package: ${cov}% >= floor ${FLOOR}%"
else
  echo "::error::root package coverage ${cov}% is below floor ${FLOOR}%"
  exit 1
fi
echo "coverage gate PASSED"
