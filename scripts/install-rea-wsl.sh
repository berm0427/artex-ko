#!/usr/bin/env bash
# Install REA's Linux analysis dependencies for ARTEX in the current WSL user.
# SPDX-License-Identifier: AGPL-3.0-or-later
set -euo pipefail

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo 'Ubuntu WSL x86_64 is required.' >&2
  exit 1
fi

# Java and download utilities are Ubuntu packages; Node, REA and Ghidra live
# under the user's home directory so rerunning this script does not replace
# machine-wide installations.
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y openjdk-21-jdk curl ca-certificates python3 xz-utils unzip

install_root="$HOME/.local/opt"
mkdir -p "$install_root" "$HOME/.local/rea"

node_version="$(curl --fail --silent --show-error https://nodejs.org/dist/index.json | python3 -c 'import json,sys; print(next(x["version"] for x in json.load(sys.stdin) if x["version"].startswith("v22.")))')"
node_dir="$install_root/node-$node_version"
if [[ ! -x "$node_dir/bin/node" ]]; then
  temp_dir="$(mktemp -d)"
  trap 'rm -rf -- "$temp_dir"' EXIT
  archive="node-$node_version-linux-x64.tar.xz"
  base="https://nodejs.org/dist/$node_version"
  curl --fail --location --silent --show-error "$base/$archive" -o "$temp_dir/$archive"
  curl --fail --location --silent --show-error "$base/SHASUMS256.txt" -o "$temp_dir/SHASUMS256.txt"
  (cd "$temp_dir" && grep "  $archive$" SHASUMS256.txt | sha256sum --check --status)
  tar -xJf "$temp_dir/$archive" -C "$install_root"
  mv "$install_root/node-$node_version-linux-x64" "$node_dir"
  rm -rf -- "$temp_dir"
  trap - EXIT
fi
export PATH="$node_dir/bin:$PATH"

rea_entry="$HOME/.local/rea/node_modules/rea-agents/scripts/rea.mjs"
if [[ ! -f "$rea_entry" ]]; then
  npm install --prefix "$HOME/.local/rea" --no-audit --no-fund rea-agents@6.4.0
fi
test -f "$rea_entry"

ghidra_dir="$install_root/ghidra_12.1.4_PUBLIC"
if [[ ! -f "$ghidra_dir/support/analyzeHeadless" ]]; then
  temp_dir="$(mktemp -d)"
  trap 'rm -rf -- "$temp_dir"' EXIT
  archive='ghidra_12.1.4_PUBLIC_20260921.zip'
  curl --fail --location --silent --show-error \
    "https://github.com/NationalSecurityAgency/ghidra/releases/download/Ghidra_12.1.4_build/$archive" \
    -o "$temp_dir/$archive"
  echo "ddac49f903da9d5bac833e5cc79395098b9c33cfd3279be5f31bd00387d2d4db  $temp_dir/$archive" | sha256sum --check --status
  unzip -q "$temp_dir/$archive" -d "$install_root"
  rm -rf -- "$temp_dir"
  trap - EXIT
fi
test -f "$ghidra_dir/support/analyzeHeadless"
"$node_dir/bin/node" --version
java -version
echo "REA dependencies ready: $rea_entry, $ghidra_dir"
