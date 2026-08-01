#!/usr/bin/env bash
set -eu

source "$(dirname "$0")/atte_lib.sh"

run golangci-lint run
