#!/usr/bin/env bash
# Sets up a CircleCI machine to run a devenv instance
set -e

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

# shellcheck source=../../lib/logging.sh
source "$DIR/../../lib/logging.sh"

info "Setting up devenv"
# Pull the devenv out of the container
docker run --rm --entrypoint bash gcr.io/outreach-docker/devenv:v1.22.0 -c 'cat "$(command -v devenv)"' >devenv

# Allow the devenv to update itself
info "Updating devenv (if needed)"
chmod +x ./devenv
./devenv --force-update-check status || true
./devenv --version

info "Moving devenv into PATH"
sudo mv devenv /usr/local/bin/devenv
sudo chown circleci:circleci /usr/local/bin/devenv

info "Setting up Git"
git config --global user.name "CircleCI E2E Test"
git config --global user.email "circleci@outreach.io"
