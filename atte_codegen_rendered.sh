#!/usr/bin/env bash

source ./atte_lib.sh

run atte config show --working-tree --format=hcl >atte_rendered.hcl
