#!/bin/sh
# Bypass test (scenario S5). Run before every demo and show it on stage.
#
# From inside the agent container, every direct call must fail: the attacker
# sink, the secret store, the deploy target, IAM, the database, the control
# plane and the public internet, including DNS. The agent's only reachable
# peer is the broker. The same is checked for the test runner, which executes
# agent-written code. It resets the demo first (repository and targets).
#
# A check passes only when the failure means "no route": a name that does not
# resolve, a refused or timed-out connection, or (for pact-core's ports, where
# netguard is expected) a connection closed without an answer. Any response,
# even an HTTP error, means the host was reachable and fails the test.
set -u
cd "$(dirname "$0")/.."
API=${API:-http://127.0.0.1:8080}
fail=0

ip_of() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$(docker compose ps -q "$1")" 2>/dev/null | awk '{print $1}'; }
report() { # ok|FAIL, description, detail
  echo "$1$(printf '%*s' $((7 - ${#1})) '')$2  -> $(echo "$3" | tail -1 | cut -c1-70)"
  [ "$1" = ok ] || fail=1
}

# curl exit codes: 6 cannot resolve, 7 cannot connect, 28 timed out,
# 52 empty reply and 56 connection reset (netguard closing the connection).
blocked_curl() { # description, service, url, extra accepted exit codes
  out=$(docker compose exec -T "$2" curl -sS -m 3 -o /dev/null "$3" 2>&1); code=$?
  case " 6 7 28 ${4:-} " in
    *" $code "*) report ok "$1" "$out" ;;
    *) report FAIL "$1" "reachable or not tested (exit $code) $out" ;;
  esac
}

# The same in Python, for the runner and for raw TCP and DNS checks. Exit 3
# means blocked; anything else (an answer, a crash, a stopped container) fails.
PY='import socket, sys, urllib.request, urllib.error
kind, target = sys.argv[1], sys.argv[2]
try:
    if kind == "url":
        urllib.request.urlopen(target, timeout=3)
    elif kind == "tcp":
        host, port = target.rsplit(":", 1)
        socket.create_connection((host, int(port)), timeout=3).close()
    elif kind == "dns":
        socket.getaddrinfo(target, 443)
except urllib.error.HTTPError as e:
    print("answered:", e); sys.exit(0)
except (OSError, ValueError) as e:
    print(e); sys.exit(3)
print("reachable"); sys.exit(0)'
blocked_py() { # description, service, kind, target
  out=$(docker compose exec -T "$2" python -c "$PY" "$3" "$4" 2>&1); code=$?
  if [ $code -eq 3 ]; then report ok "$1" "$out"; else report FAIL "$1" "reachable or not tested (exit $code) $out"; fi
}

curl -fsS -X POST -H 'X-PACT-Client: cli' "$API/v1/demo/reset" -d '{}' >/dev/null || { echo "control plane not reachable at $API; is the stack up?"; exit 2; }

SINK_IP=$(ip_of sink); SECRETS_IP=$(ip_of secrets)
[ -n "$SINK_IP" ] && [ -n "$SECRETS_IP" ] || { echo "the sink or secrets container is not running: docker compose ps"; exit 2; }

echo "From the agent container (agent_net only):"
blocked_curl "curl http://sink:8000 (attacker host, by name)"    agent http://sink:8000/steal
blocked_curl "curl http://$SINK_IP:8000 (attacker host, by IP)"  agent "http://$SINK_IP:8000/steal"
blocked_curl "curl http://secrets:8000 (secret store, by name)"  agent http://secrets:8000/secrets/prod/db-password
blocked_curl "curl http://$SECRETS_IP:8000 (secret store, by IP)" agent "http://$SECRETS_IP:8000/secrets/prod/db-password"
blocked_curl "curl http://deploy:8000 (deploy target)"            agent http://deploy:8000/_health
blocked_curl "curl http://iam:8000 (IAM)"                         agent http://iam:8000/_health
blocked_py   "tcp postgres:5432 (database)"                       agent tcp postgres:5432
blocked_curl "curl http://pact-core:8080 (control plane)"         agent http://pact-core:8080/v1/status "52 56"
blocked_curl "curl https://example.com (public internet)"         agent https://example.com/
blocked_curl "curl http://1.1.1.1 (public internet, by IP)"       agent http://1.1.1.1/
blocked_py   "dns example.com (no outside name lookups)"          agent dns example.com

echo "From the test runner (runner_net only; it executes agent-written code):"
blocked_py "secret store"                 runner url http://secrets:8000/_health
blocked_py "attacker sink, by IP"         runner url "http://$SINK_IP:8000/steal"
blocked_py "deploy target"                runner url http://deploy:8000/_health
blocked_py "IAM"                          runner url http://iam:8000/_health
blocked_py "database"                     runner tcp postgres:5432
blocked_py "control plane"                runner url http://pact-core:8080/v1/status
blocked_py "broker port"                  runner url http://pact-core:9090/healthz
blocked_py "public internet, by IP"       runner url http://1.1.1.1/
blocked_py "public internet, by name"     runner url https://example.com/
blocked_py "dns example.com"              runner dns example.com

echo "Positive controls (without them, the checks above prove nothing):"
if docker compose exec -T agent curl -fsS -m 3 http://pact-core:9090/healthz >/dev/null 2>&1; then
  echo "ok     agent reaches the broker at pact-core:9090"
else
  echo "FAIL   agent cannot reach the broker"; fail=1
fi
if docker compose exec -T runner python -c 'print("up")' >/dev/null 2>&1; then
  echo "ok     runner container is up and answering"
else
  echo "FAIL   runner container is not running"; fail=1
fi

SINK_COUNT=$(curl -fsS "$API/v1/oracles" | python3 -c 'import json,sys; print(json.load(sys.stdin)["observation"]["sink_requests"])')
if [ "$SINK_COUNT" = "0" ]; then echo "ok     attacker sink log is empty"; else echo "FAIL   attacker sink received $SINK_COUNT request(s)"; fail=1; fi

if [ $fail -eq 0 ]; then echo "BYPASS TEST PASSED: the broker is the only door."; else echo "BYPASS TEST FAILED"; fi
exit $fail
