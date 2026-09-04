#!/usr/bin/env bash
set -euo pipefail

deploy_host="${DEPLOY_HOST:-uptick}"
remote_root="/opt/hackersprint2-sim"
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_dir="$(mktemp -d)"
trap 'rm -rf "${build_dir}"' EXIT

cd "${project_root}"
echo "Building linux/amd64 server..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o "${build_dir}/hackersprint2-sim" ./cmd/server

echo "Preparing ${deploy_host}:${remote_root}..."
ssh "${deploy_host}" "install -d -m 0755 '${remote_root}/bin' '${remote_root}/config' '${remote_root}/deploy'"

rsync -az --checksum "${build_dir}/hackersprint2-sim" \
  "${deploy_host}:${remote_root}/bin/.hackersprint2-sim.new"
rsync -az --checksum "${project_root}/config/world-generation.v2.yaml" \
  "${deploy_host}:${remote_root}/config/.world-generation.v2.yaml.new"
rsync -az --checksum "${project_root}/deploy/hackersprint2-sim.service" \
  "${deploy_host}:${remote_root}/deploy/.hackersprint2-sim.service.new"

ssh "${deploy_host}" "set -eu
chmod 0755 '${remote_root}/bin/.hackersprint2-sim.new'
chmod 0644 '${remote_root}/config/.world-generation.v2.yaml.new' '${remote_root}/deploy/.hackersprint2-sim.service.new'
chown root:root '${remote_root}/bin/.hackersprint2-sim.new' '${remote_root}/config/.world-generation.v2.yaml.new' '${remote_root}/deploy/.hackersprint2-sim.service.new'
mv -f '${remote_root}/bin/.hackersprint2-sim.new' '${remote_root}/bin/hackersprint2-sim'
mv -f '${remote_root}/config/.world-generation.v2.yaml.new' '${remote_root}/config/world-generation.v2.yaml'
mv -f '${remote_root}/deploy/.hackersprint2-sim.service.new' '${remote_root}/deploy/hackersprint2-sim.service'
"

echo "Synced ${deploy_host}:${remote_root}. Run 'make restart' to activate the new binary."
