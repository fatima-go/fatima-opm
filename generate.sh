#!/bin/sh
set -eu
cd "$(dirname "$0")"
protoc -I . --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. api/control.proto api/operations.proto
