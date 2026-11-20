#!/bin/sh
# Starts PACT on the Compute Engine VM behind Caddy, which adds HTTPS and a
# password. The control plane has no login of its own, so it is never exposed
# without one. Run on the VM, from the repository root:
#
#   sh deploy/gcp/up.sh
#
# Optional settings:
#   PACT_DASH_USER      dashboard user name (default: judge)
#   PACT_DASH_PASSWORD  dashboard password (asked for if unset)
#   PACT_HOST           your own domain, if its A record points at this VM
#                       (default: <external-ip>.sslip.io, which needs no DNS setup)
#   PACT_PUBLIC=1       no password at all. Anyone with the link can approve held
#                       actions, reset the demo and spend the Gemini quota; for a
#                       short demo only, then stop the VM
#
# Safe to run again, for example to change the password or pick up new code.
set -eu
cd "$(dirname "$0")/../.."

if [ ! -f /var/lib/pact-ready ]; then
  echo "The VM is still installing Docker and Caddy. Try again in a minute."
  echo "Progress: sudo journalctl -u google-startup-scripts -f"
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  if id -nG | grep -qw docker; then
    echo "Docker is not running: sudo systemctl status docker"
  else
    getent group docker | grep -qw "$(id -un)" || sudo usermod -aG docker "$(id -un)"
    echo "You are in the docker group now, but this SSH session started before that."
    echo "Type exit, connect again with gcloud compute ssh, then run:"
    echo "  cd $(pwd) && sh deploy/gcp/up.sh"
  fi
  exit 1
fi

# --- .env: stays on this VM, readable by you only ---------------------------
[ -f .env ] || cp .env.example .env
if [ ! -O .env ]; then
  echo ".env belongs to another user (edited with sudo?). Fix it with:"
  echo "  sudo chown $(id -un): .env"
  exit 1
fi
chmod 600 .env

get_env() { sed -n "s/^$1=//p" .env | tail -n 1; }
set_env() { # NAME VALUE: replace the line, or append it
  if grep -q "^$1=" .env; then sed -i "s|^$1=.*|$1=$2|" .env; else printf '%s=%s\n' "$1" "$2" >> .env; fi
}

# The defaults in .env.example are public. Replace them before the first start;
# POSTGRES_PASSWORD only takes effect when the database volume is created.
for v in TARGET_TOKEN RUNNER_TOKEN POSTGRES_PASSWORD; do
  case "$(get_env "$v")" in
    "" | dev-target-token | dev-runner-token | pact-dev)
      if [ "$v" = POSTGRES_PASSWORD ] && docker volume inspect pact_pgdata >/dev/null 2>&1; then
        echo "note   keeping POSTGRES_PASSWORD: the database already exists and was created with it"
        continue
      fi
      set_env "$v" "$(openssl rand -hex 16)"
      echo "ok     random $v written to .env" ;;
  esac
done

[ -n "$(get_env GEMINI_API_KEY)" ] || echo "note   GEMINI_API_KEY is empty: pre-written contract and scripted agent only (add it with: nano .env, then rerun)"

# --- Caddy: HTTPS for the host name, a password, then 127.0.0.1:8080 --------
if [ -z "${PACT_HOST:-}" ]; then
  IP=$(curl -fsS -H 'Metadata-Flavor: Google' \
    http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip)
  PACT_HOST=$(echo "$IP" | tr . -).sslip.io
fi
# pact-core answers only to host names it knows (DNS-rebinding protection);
# sslip.io names are known already, a domain of your own is added here.
case "$PACT_HOST" in
  "" | *[!A-Za-z0-9.-]*) echo "PACT_HOST must be a host name (letters, digits, dots and hyphens)"; exit 1 ;;
  *.sslip.io | *.nip.io) ;;
  *) set_env PACT_ALLOWED_HOSTS "$PACT_HOST" ;;
esac
DASH_USER=${PACT_DASH_USER:-judge}
PASS=""
AUTH=""
if [ "${PACT_PUBLIC:-}" = 1 ]; then
  echo "note   PACT_PUBLIC=1: no password. Anyone with the link can approve held actions,"
  echo "       reset the demo and spend the Gemini quota. Stop the VM when you are done."
else
  case "$DASH_USER" in "" | *[!A-Za-z0-9_.-]*) echo "PACT_DASH_USER may use letters, digits, _ . - only"; exit 1 ;; esac
  PASS=${PACT_DASH_PASSWORD:-}
  if [ -z "$PASS" ]; then
    [ -t 0 ] || { echo "Set PACT_DASH_PASSWORD, or run this in a terminal to be asked for it."; exit 1; }
    trap 'stty echo 2>/dev/null' EXIT
    trap 'exit 130' INT
    printf 'Dashboard password for %s (10+ characters; PACT_PUBLIC=1 for none): ' "$DASH_USER"
    stty -echo; read -r PASS; stty echo; echo
  fi
  [ ${#PASS} -ge 10 ] || { echo "Use a password of at least 10 characters."; exit 1; }
  HASH=$(caddy hash-password --plaintext "$PASS")
  AUTH=$(printf '\tbasic_auth {\n\t\t%s %s\n\t}' "$DASH_USER" "$HASH")
fi

# No `encode` directive: compression would buffer the dashboard's live event stream.
sudo tee /etc/caddy/Caddyfile >/dev/null <<EOF
# Written by deploy/gcp/up.sh: HTTPS for $PACT_HOST, then the PACT control plane
# on the loopback port compose publishes. 9090 is published nowhere.
$PACT_HOST {
$AUTH
	reverse_proxy 127.0.0.1:8080
}
EOF
sudo systemctl reload caddy
if [ -n "$AUTH" ]; then
  echo "ok     Caddy serves https://$PACT_HOST behind a password"
else
  echo "ok     Caddy serves https://$PACT_HOST with no password"
fi

# --- PACT ------------------------------------------------------------------
echo "Building and starting PACT (the first build takes about 10 minutes)..."
docker compose up -d --build --wait --remove-orphans

curl -fsS -o /dev/null http://127.0.0.1:8080/healthz
echo "ok     pact-core healthy on 127.0.0.1:8080"

# Caddy fetches the certificate in the background after the reload; allow for it.
if [ -n "$AUTH" ]; then set -- -u "$DASH_USER:$PASS"; else set --; fi
i=0
until curl -fsS -o /dev/null "$@" "https://$PACT_HOST/healthz" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -ge 30 ]; then
    echo "FAIL   https://$PACT_HOST does not answer. See: sudo journalctl -u caddy -n 50"
    exit 1
  fi
  sleep 3
done
echo "ok     https://$PACT_HOST answers"

if [ -n "$AUTH" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' "https://$PACT_HOST/healthz")
  if [ "$code" = 401 ]; then
    echo "ok     https://$PACT_HOST refuses requests without the password (401)"
  else
    echo "FAIL   https://$PACT_HOST answered $code without a password; expected 401"
    exit 1
  fi
  WHO="(user: $DASH_USER)"
else
  WHO="(public, no password)"
fi

cat <<EOF

PACT is up.
  Dashboard  https://$PACT_HOST   $WHO
  Next       make bypass-test && make replay
EOF
