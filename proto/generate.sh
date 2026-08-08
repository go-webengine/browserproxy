#!/usr/bin/env bash
# Copyright (c) the go-webengine/browserproxy authors.
# SPDX-License-Identifier: BSD-3-Clause
#
# Regenerates browserpb/*.pb.go from proto/browser.proto. Requires protoc plus
# the Go plugins (installed on demand into GOBIN). Run from the module root:
#     bash proto/generate.sh
set -euo pipefail

cd "$(dirname "$0")/.."

GOBIN="$(go env GOPATH)/bin"
export PATH="$GOBIN:$PATH"

need() { command -v "$1" >/dev/null 2>&1 || go install "$2"; }
need protoc-gen-go      google.golang.org/protobuf/cmd/protoc-gen-go@latest
need protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --go_opt=Mproto/browser.proto=github.com/go-webengine/browserproxy/browserpb \
  --go-grpc_opt=Mproto/browser.proto=github.com/go-webengine/browserproxy/browserpb \
  proto/browser.proto

# protoc emits alongside the .proto (source_relative); move into the package dir.
mv -f proto/browser.pb.go proto/browser_grpc.pb.go browserpb/
echo "generated browserpb/browser.pb.go browserpb/browser_grpc.pb.go"
