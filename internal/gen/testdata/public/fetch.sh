#!/bin/sh
# Fetches the large public specs gen's tests check, pinned to a commit,
# into .cache/ (git-ignored). The tests skip a spec that isn't there.
#
#   go generate ./internal/gen
set -eu
cd "$(dirname "$0")"
mkdir -p .cache
fetch() { # name url
	if [ ! -s ".cache/$1" ]; then
		echo "fetching $1"
		curl -sSfL --max-time 300 -o ".cache/$1.tmp" "$2"
		mv ".cache/$1.tmp" ".cache/$1"
	fi
}
# MIT, https://github.com/github/rest-api-description
fetch github.yaml https://raw.githubusercontent.com/github/rest-api-description/320307e220815d528224ca891cf8a9e3c0243678/descriptions/api.github.com/api.github.com.yaml
# MIT, https://github.com/stripe/openapi
fetch stripe.yaml https://raw.githubusercontent.com/stripe/openapi/51610f5f061c9723730582a7638b8cc0e646640d/openapi/spec3.yaml
