#!/usr/bin/env bash
set -euo pipefail

action="${1:-}"
deploy_host="${DEPLOY_HOST:-uptick}"
service_name="hackersprint2-sim.service"

case "${action}" in
  start|restart)
    ssh "${deploy_host}" "systemctl '${action}' '${service_name}' && systemctl --no-pager --full status '${service_name}'"
    ;;
  stop)
    ssh "${deploy_host}" "systemctl stop '${service_name}' && if systemctl is-active --quiet '${service_name}'; then exit 1; else echo '${service_name} stopped'; fi"
    ;;
  logs)
    log_lines="${LOG_LINES:-200}"
    if [[ ! "${log_lines}" =~ ^[0-9]+$ ]]; then
      echo "LOG_LINES must be a non-negative integer" >&2
      exit 2
    fi
    exec ssh "${deploy_host}" "journalctl -u '${service_name}' -n '${log_lines}' -f --no-pager"
    ;;
  *)
    echo "usage: $0 {start|stop|restart|logs}" >&2
    exit 2
    ;;
esac
