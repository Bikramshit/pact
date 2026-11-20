#!/bin/sh
# Compute Engine startup script for the PACT demo VM (Ubuntu 24.04, amd64).
# create-vm.sh attaches it as instance metadata and the VM runs it as root at
# every boot. The first boot installs Docker Engine with the compose plugin and
# Caddy; later boots find the marker file and do nothing.
#
# Progress:  sudo journalctl -u google-startup-scripts -f   (ends with "pact: VM ready")
set -eu

READY=/var/lib/pact-ready
[ -f "$READY" ] && exit 0

export DEBIAN_FRONTEND=noninteractive HOME="${HOME:-/root}"
apt-get update
apt-get install -y --no-install-recommends ca-certificates curl gnupg git make python3 openssl

# Docker Engine and the compose plugin, from Docker's own repository: compose.yaml
# uses `include:`, which needs Compose 2.20 or later.
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
# shellcheck source=/dev/null
CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME")
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $CODENAME stable" \
  > /etc/apt/sources.list.d/docker.list

# Caddy terminates HTTPS and asks for the dashboard password; up.sh writes its config.
curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
  | gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
chmod a+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list

apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin caddy

# Until up.sh runs, port 80 says so instead of showing Caddy's welcome page.
cat > /etc/caddy/Caddyfile <<'EOF'
:80 {
	respond "PACT is not configured yet. Run deploy/gcp/up.sh on the VM." 503
}
EOF

systemctl enable --now docker caddy
systemctl reload caddy

touch "$READY"
echo "pact: VM ready"
