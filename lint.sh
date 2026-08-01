#!/bin/sh
set -eu

nix develop --command golangci-lint run
