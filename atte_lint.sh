#!/usr/bin/env bash
set -eu

source ./atte_lib.sh

run golangci-lint run
