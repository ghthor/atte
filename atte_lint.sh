#!/usr/bin/env bash
set -eu

source ./atte_lib.sh

run golangci-lint run
run golangci-lint run --enable-only=funcorder ./detector/attehcl/...
