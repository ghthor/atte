#!/usr/bin/env bash
set -eu

export ATTE_CODEGEN=1
go test -count=1 ./...
