#!/usr/bin/env bash
# Serves the console (:3000) and Grafana (:3001) on this machine's Tailscale
# address only, through systemd user services that restart port-forwards when
# pods are replaced and come back after a reboot (needs `loginctl
# enable-linger`). There is no login on the console: only your tailnet can
# reach it.
#   scripts/tailscale-expose.sh install|uninstall|status
set -euo pipefail
units="$HOME/.config/systemd/user"
kubectl="$(command -v kubectl)"
tailscale="$(command -v tailscale)"

unit() { # name service local:remote
  cat >"$units/unfraud-$1.service" <<UNIT
[Unit]
Description=unfraud $1 on the Tailscale address
After=network-online.target

[Service]
# The address is looked up at start, so a changed Tailscale IP is picked up.
ExecStart=/bin/sh -c 'exec $kubectl --context kind-unfraud -n unfraud port-forward --address "\$($tailscale ip -4)" svc/$2 $3'
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
}

case "${1:-install}" in
install)
  mkdir -p "$units"
  unit console web 3000:3000
  unit grafana grafana 3001:3000
  # Bring the kind node back with Docker after a reboot.
  docker update --restart=unless-stopped unfraud-control-plane >/dev/null
  systemctl --user daemon-reload
  systemctl --user enable --now unfraud-console.service unfraud-grafana.service
  ip=$($tailscale ip -4)
  echo "console: http://$ip:3000"
  echo "grafana: http://$ip:3001"
  ;;
uninstall)
  systemctl --user disable --now unfraud-console.service unfraud-grafana.service || true
  rm -f "$units/unfraud-console.service" "$units/unfraud-grafana.service"
  systemctl --user daemon-reload
  ;;
status)
  systemctl --user --no-pager status unfraud-console.service unfraud-grafana.service | grep -E "●|Active:"
  ;;
esac
