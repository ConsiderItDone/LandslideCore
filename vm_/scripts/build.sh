#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# Load the constants
# Set the PATHS
GOPATH="$(go env GOPATH)"


if [[ $# -eq 1 ]]; then
    BINARY_PATH=$1
elif [[ $# -eq 0 ]]; then
    BINARY_PATH="$GOPATH/src/github.com/ava-labs/avalanchego/build/plugins/pjSL9ksard4YE96omaiTkGL5H6XX2W5VEo3ZgWC9S2P6gzs9A"
else
    echo "Invalid arguments to build landslide. Requires zero (default location) or one argument to specify binary location."
    exit 1
fi


# Build landslidevm, which is run as a subprocess
echo "Building landslidevm in $BINARY_PATH"
go build -o "$BINARY_PATH" ./vm/cmd/...
