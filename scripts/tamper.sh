#!/bin/sh
# Tamper demo (scenario T3): edit one stored evidence record directly in
# PostgreSQL, the way someone with database access would. Then `make verify`
# (or the Evidence page) reports the sequence number of the edited record.
#
#   make tamper          turn the most recent "deny" record into an "allow"
#   make tamper SEQ=5    change the outcome of record 5
#   make truncate        delete the newest record
#   make untamper        put the original record(s) back (for rehearsals)
set -eu
cd "$(dirname "$0")/.."
psql() { docker compose exec -T -e PGOPTIONS=--client-min-messages=warning postgres psql -q -U pact -d pact -At -c "$1"; }

# Keep the original text so `make untamper` can put it back between rehearsals.
psql "CREATE TABLE IF NOT EXISTS tamper_backup (seq BIGINT PRIMARY KEY, record_json TEXT NOT NULL)" >/dev/null
backup() { psql "INSERT INTO tamper_backup SELECT seq, record_json FROM evidence WHERE seq = $1 ON CONFLICT DO NOTHING" >/dev/null; }

psql "CREATE TABLE IF NOT EXISTS tamper_deleted (LIKE evidence)" >/dev/null

SEQ=${1:-}
if [ "$SEQ" = "truncate" ]; then
  # Delete the newest record. The remaining chain is internally consistent; only
  # the signed checkpoint, kept outside the database, shows that one is missing.
  N=$(psql "WITH gone AS (DELETE FROM evidence WHERE seq = (SELECT max(seq) FROM evidence) RETURNING *) INSERT INTO tamper_deleted SELECT * FROM gone RETURNING seq")
  [ -n "$N" ] || { echo "the chain is empty"; exit 1; }
  echo "record $N: deleted from the database (the newest record)"
  echo "now run: make verify"
  exit 0
fi
if [ "$SEQ" = "restore" ]; then
  D=$(psql "WITH back AS (DELETE FROM tamper_deleted RETURNING *) INSERT INTO evidence SELECT * FROM back ON CONFLICT DO NOTHING RETURNING seq" | tr '\n' ' ')
  [ -n "$D" ] && echo "put back deleted record(s): $D"
  N=$(psql "UPDATE evidence e SET record_json = b.record_json FROM tamper_backup b WHERE e.seq = b.seq RETURNING e.seq" | tr '\n' ' ')
  psql "DELETE FROM tamper_backup" >/dev/null
  echo "restored record(s): ${N:-none}"
  exit 0
fi
if [ -z "$SEQ" ]; then
  SEQ=$(psql "SELECT seq FROM evidence WHERE record_json LIKE '%\"decision\":\"deny\"%' ORDER BY seq DESC LIMIT 1")
  [ -n "$SEQ" ] || { echo "no deny record in the chain yet; run a scenario first or pass SEQ=n"; exit 1; }
  backup "$SEQ"
  psql "UPDATE evidence SET record_json = replace(record_json, '\"decision\":\"deny\"', '\"decision\":\"allow\"') WHERE seq = $SEQ" >/dev/null
  echo "record $SEQ: decision changed from deny to allow in the database"
else
  case "$SEQ" in *[!0-9]*) echo "SEQ must be a number"; exit 1;; esac
  backup "$SEQ"
  N=$(psql "UPDATE evidence SET record_json = regexp_replace(record_json, '\"outcome\":\"[a-z_]+\"', '\"outcome\":\"tampered\"') WHERE seq = $SEQ RETURNING seq")
  [ -n "$N" ] || { echo "no record with seq $SEQ"; exit 1; }
  echo "record $SEQ: outcome overwritten in the database"
fi
echo "now run: make verify"
