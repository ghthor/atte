#!/usr/bin/env bash

source ./atte_lib.sh

run atte config show --format=hcl >atte_rendered.hcl
