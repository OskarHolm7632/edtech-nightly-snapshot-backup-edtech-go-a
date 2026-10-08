#!/bin/sh
set -eu

go run ./cmd/nightly-snapshot -once -input sample-data.json -bucket edtech-nightly
