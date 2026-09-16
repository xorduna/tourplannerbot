#!/usr/bin/env bash
set -euo pipefail

goose_version="${GOOSE_VERSION:-v3.25.0}"
goose_install_directory="${GOOSE_INSTALL:-${HOME}/.goose}"

echo "Installing Goose ${goose_version}."
curl -fsSL "https://raw.githubusercontent.com/pressly/goose/${goose_version}/install.sh" \
  | GOOSE_INSTALL="${goose_install_directory}" sh -s -- "${goose_version}"
