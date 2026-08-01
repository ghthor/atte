#!/usr/bin/env bash

run() {
  if command -v "$1" &>/dev/null; then
    command "$@"
  else
    nix develop --command "$@"
  fi
}
