# API reference

`pact-core` listens on two ports, and each refuses connections arriving from the wrong network.

| Port | Who reaches it | What it serves |
| --- | --- | --- |
| **8080**, control plane | The user, on `127.0.0.1` only | The dashboard, contracts, holds, evidence, scoreboard |
| **9090**, broker | The agent, on `agent_net` only | Tool calls, the model proxy, agent enrolment |

All bodies are JSON. Errors are `{"error": "…"}` with a 4xx or 5xx status.

## Broker (port 9090)

### `POST /v1/tools/{tool}`

One protected action. It must carry four headers:

| Header | Value |
| --- | --- |
| `X-PACT-Capability` | The capability token, `base64url(payload).base64url(sig)` |
| `X-PACT-Timestamp` | RFC 3339 UTC, within 60 seconds of the broker's clock |
| `X-PACT-Nonce` | 8 to 128 characters, never reused |
| `X-PACT-Signature` | `ed25519:…` over `METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nNONCE`, by the agent key the capability is bound to |

Body: `{"args": {...}}`. The broker derives the resource from the tool and its arguments:

| Tool | Arguments | Resource : operation |
| --- | --- | --- |
| `list_files` | `path` | `repo:web-app/<path>:read` |
| `read_file` | `path` | `repo:web-app/<path>:read` |
| `write_file` | `path`, `content` | `repo:web-app/<path>:write` |
| `run_tests` | | `tests:web-app:run` |
| `deploy` | | `deploy:web-app:deploy` |
| `read_secret` | `name` | `secret:<name>:read` |
| `http_request` | `url`, `method`, `body` | `net:<host>:<METHOD>` |
| `create_user` | `username`, `role` | `iam:user:create_<role>` |

Responses:

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `{"decision": "allow", "decision_id", "result"}` | Executed (`error` is set if the action itself failed) |
| 200 | `{..., "held": true, "hold_id"}` | Held, then approved by a human, then executed |
| 403 | `{"decision": "deny", "reasons", "decision_id", "checks"}` | Denied; nothing executed |
| 403 | `{"decision": "deny", "held": true, "reasons": ["hold_rejected", …]}` | Held, then rejected or timed out |
| 500 | `{"error"}` | The evidence record could not be written, so no result is released |

Reason codes include `contract_deny: <pattern>`, `no_capability_scope: <key>`, `not_in_contract: <key>`, `tainted_session`, `confidential_session`, `approval_required`, `taint_tolerant`, `bad_capability_signature`, `bad_request_signature`, `nonce_reused`, `stale_timestamp`, `capability_expired`, `capability_revoked`, `unknown_capability`, `missing_header: <name>` and `invalid_request: <detail>`.

### `POST /v1/llm/generate`

The Gemini proxy. It needs the same four headers and the `llm:gemini:generate` scope. Body: `{"request": <generateContent request>}`. Only text and function declarations are forwarded; provider-side tools and file URIs are refused. The response is Gemini's `generateContent` response.

### Agent enrolment and work

| Endpoint | Does |
| --- | --- |
| `POST /v1/agents` `{"pubkey": "ed25519:…"}` | Registers the agent's public key. Returns the agent id, which is derived from the key. |
| `GET /v1/agents/{id}/assignment` | Long-polls (25 seconds) for work. Returns 204 when there is none. The assignment carries the capability, the instruction and, for scripted runs, the steps. |
| `POST /v1/agents/{id}/assignments/{aid}/done` | The agent's report. It is untrusted; scores come from the targets. |

## Control plane (port 8080)

### Status and keys

| Endpoint | Returns |
| --- | --- |
| `GET /v1/status` | Mode, policy engine, model and fallbacks, models used, key backend, agents online, store |
| `GET /v1/keys` | Org, issuer and evidence public keys; their backends (local or Cloud KMS); whether the org key's certificate for the evidence key verifies |
| `GET /v1/stats` | Counts of records, allowed, denied, held, approved, rejected, model calls, taint reads, tasks |

### Contracts and tasks

| Endpoint | Does |
| --- | --- |
| `POST /v1/contracts/draft` `{"instruction", "template"?}` | Gemini drafts a candidate (or `template: true` loads the pre-written one). Returns the contract, plain-language summary, ambiguities, warnings, and who drafted it. Nothing is signed. |
| `POST /v1/contracts/{id}/confirm` `{"confirmed_by", "agent_id"?, "contract"?}` | Validates, binds to the agent, hashes and signs with the org key. Immutable afterwards. |
| `GET /v1/contracts`, `GET /v1/contracts/{id}` | Confirmed contracts, signature verified on read |
| `POST /v1/capabilities` `{"contract_id"}` | Mints a capability for a confirmed contract |
| `POST /v1/capabilities/{id}/revoke` | Revokes it; later requests are denied with `capability_revoked` |
| `POST /v1/tasks` `{"contract_id", "kind": "live"\|"replay", "scenario"?, "variant"?, "reset"?}` | Mints a capability and hands the task to the agent |
| `GET /v1/tasks` | Assignments and their status and reports |
| `GET /v1/sessions`, `GET /v1/sessions/{id}` | Per-capability state: tainted, taint sources, confidential |

### Decisions, holds and evidence

| Endpoint | Does |
| --- | --- |
| `GET /v1/decisions/stream` | Server-sent events (see below) |
| `GET /v1/holds` | Pending and recently resolved holds, with diff, taint sources and confidential sources |
| `POST /v1/holds/{id}/approve` `{"by"}` | Executes the held action; the approver is recorded |
| `POST /v1/holds/{id}/reject` `{"by"}` | Rejects it; nothing executes |
| `GET /v1/evidence?task_id=&limit=&from_seq=` | Records in sequence order, plus `total` |
| `GET /v1/evidence/verify` | `{ok, count, first_bad_seq, problem, truncated, checkpoint, head_hash, public_key}` |
| `GET /v1/evidence/export` | A self-contained bundle: records as stored, checkpoint, evidence key, org key and certificate. Verify it with `pact-core verify FILE [-org-pubkey …]`. |

### Scenarios, scoreboard and red-team

| Endpoint | Does |
| --- | --- |
| `GET /v1/scenarios` | The seed scenarios and their variants |
| `GET /v1/oracles` | The targets' own state: secret reads, sink requests, pipeline hash, admin accounts, deployments |
| `POST /v1/demo/reset` `{"scenario"?, "variant"?}` | Resets the repository (optionally to a scenario's files) and clears the targets |
| `POST /v1/replay` `{"scenarios"?}` | Runs scripted scenarios with PACT on, checks every step and target |
| `POST /v1/scoreboard/runs` `{"agent", "quick", "supervisor"}` | Starts a run in the background |
| `GET /v1/scoreboard/latest`, `GET /v1/scoreboard/runs`, `GET /v1/scoreboard/runs/{id}` | The current or latest run, the history, one stored run |
| `GET /v1/redteam` | The README a red-team run starts from, the two task wordings, the latest run |
| `POST /v1/redteam` `{"injection", "plain"?, "supervisor"?, "interactive"?}` | Plants the text in the untrusted README and runs the live agent in each configuration |

### Live events

`GET /v1/decisions/stream` sends server-sent events. Each event is `{"type", "at", "data"}`:

| Event | Data |
| --- | --- |
| `decision` | A new evidence record |
| `hold_opened`, `hold_resolved` | The hold |
| `taint` | Session, resource and hash when a session becomes tainted |
| `session`, `agent`, `contract` | A new session, agent registration or signed contract |
| `assignment` | A task's status changed |
| `unprotected` | A call executed with PACT off, or reviewed by the AI supervisor (no evidence is written in those modes) |
| `scoreboard`, `redteam` | Run progress |

## Example: one denied call, by hand

From inside the agent container you can only reach the broker. Its own client signs requests for you. To see a denial without signing anything, call the broker with no headers; it is denied and recorded:

```sh
docker compose exec agent curl -s -X POST http://pact-core:9090/v1/tools/read_secret \
  -d '{"args":{"name":"prod/db-password"}}'
# {"checks":{},"decision":"deny","decision_id":"d-…","reasons":["missing_header: X-PACT-Capability"]}
```

The same attempt is now the newest record on the Evidence page.
