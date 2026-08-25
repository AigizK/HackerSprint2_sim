#!/usr/bin/env bash
set -euo pipefail

deploy_host="${DEPLOY_HOST:-uptick}"
service_name="hackersprint2-sim"
remote_root="/opt/hackersprint2-sim"

ssh "${deploy_host}" "set -eu
if ! getent group '${service_name}' >/dev/null; then
  groupadd --system '${service_name}'
fi
if ! id '${service_name}' >/dev/null 2>&1; then
  useradd --system --gid '${service_name}' --home-dir /var/lib/${service_name} --no-create-home --shell /usr/sbin/nologin '${service_name}'
fi
install -d -o '${service_name}' -g '${service_name}' -m 0750 /var/lib/${service_name}
systemd-analyze verify '${remote_root}/deploy/${service_name}.service'
install -o root -g root -m 0644 '${remote_root}/deploy/${service_name}.service' '/etc/systemd/system/${service_name}.service'
systemctl daemon-reload
systemctl enable '${service_name}.service'
"

echo "Installed and enabled ${service_name}. Run 'make start' to launch it."
