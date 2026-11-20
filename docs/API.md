# API reference

`pact-core` listens on two ports, and each refuses connections arriving from the wrong network.

| Port | Who reaches it | What it serves |
| --- | --- | --- |
| **8080**, control plane | The user, on `127.0.0.1` only | The dashboard, contracts, holds, evidence, scoreboard |
| **9090**, broker | The agent, on `agent_net` only | Tool calls, the model proxy, agent enrolment |

All bodies are JSON. Errors are `{"error": "…"}` with a 4xx or 5xx status. Both ports answer `GET /healthz` with `{"ok": true}`.

**Every state-changing control-plane request** (`POST`, `PUT`, `DELETE` under `/v1/`) must carry an `X-PACT-Client` header (any value) and must not be marked `Sec-Fetch-Site: cross-site`; otherwise it is refused with 403. This stops another website from acting through a signed-in browser. The dashboard and `scripts/pact.py` send it; add it to your own scripts: `curl -H 'X-PACT-Client: cli' -X POST …`. Reads need no header.

**The control plane answers only to known host names**: `localhost`, IP addresses, `*.sslip.io`, `*.nip.io` and the names in `PACT_ALLOWED_HOSTS`. Any other `Host` gets 421, so a page that rebinds its own DNS name to `127.0.0.1` cannot use the API.

## Broker (port 9090)

### `POST /v1/tools/{tool}`

One protected action. It must carry four headers:

| Header | Value |
| --- | --- |
| `X-PACT-Capability` | The capability token, `base64url(payload).base64url(sig)` |
| `X-PACT-Timestamp` | RFC 3339 UTC, within 60 seconds of the broker's clock |
| `X-PACT-Nonce` | 8 to 128 characters, never reused by the same agent key (under any capability) |
| `X-PACT-Signature` | `ed25519:…` over `METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nNONCE`, by the agent key the capability is bound to |

Body: `{"args": {...}}`, at most 1 MiB (413 above). The broker derives the resource from the tool and its arguments:

| Tool | Arguments | Resource : operation |
| --- | --- | --- |
| `list_files` | `path` | `repo:web-app/<path>:read` |
| `read_file` | `path` | `repo:web-app/<path>:read` |
| `write_file` | `path`, `content` | `repo:web-app/<path>:write` |
| `run_tests` | | `tests:web-app:run` |
| `deploy` | | `deploy:web-app:deploy` |
| `read_secret` | `name` | `secret:<name>:read` |
| `http_request` | `url` (http or https, standard port only), `method`, `body` | `net:<host>:<METHOD>` |
| `create_user` | `username`, `role` | `iam:user:create_<role>` |

Responses:

| Status | Body | Meaning |
| --- | --- | --- |
| 200 | `{"decision": "allow", "decision_id", "result"}` | Executed (`error` is set if the action itself failed) |
| 200 | `{..., "held": true, "hold_id"}` | Held, then approved by a human, then executed |
| 403 | `{"decision": "deny", "reasons", "decision_id", "checks"}` | Denied; nothing executed |
| 403 | `{"decision": "deny", "held": true, "reasons": ["hold_rejected", …]}` | Held, then rejected (`hold_rejected`) or timed out (`hold_timeout`); or approved after the capability was revoked or expired (`capability_revoked`, …), and not executed |
| 500 | `{"error"}` | The evidence record could not be written, so no result is released. Also returned for every call while the stored chain is behind its signed checkpoint (records were deleted) |

Reason codes include `contract_deny: <pattern>`, `no_capability_scope: <key>`, `not_in_contract: <key>`, `tainted_session`, `confidential_session`, `approval_required`, `taint_tolerant`, `bad_capability_signature`, `bad_request_signature`, `bad_nonce`, `nonce_reused`, `stale_timestamp`, `capability_expired`, `capability_revoked`, `unknown_capability`, `contract_unverifiable`, `contract_expired`, `agent_mismatch`, `missing_header: <name>`, `invalid_request: <detail>` and `internal_error: <detail>`. In AI-supervisor mode a blocked call carries `supervisor_blocked: <reason>`.

### `POST /v1/llm/generate`

The Gemini proxy. It needs the same four headers and the `llm:gemini:generate` scope. Body: `{"request": <generateContent request>}`, at most 8 MiB. Only the fields the agent needs are accepted (`contents`, `systemInstruction`, `tools` with `functionDeclarations` only, `toolConfig`, `generationConfig`, `safetySettings`). Provider-side tools, `fileData` anywhere in the request, repeated keys and trailing data are refused. The broker forwards its own re-encoding of the checked request. The response is Gemini's `generateContent` response.

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
| `GET /v1/stats` | Counts of records, allowed, denied, held, approved, rejected (including timed out and auto-rejected), model calls, taint reads, tasks |
| `GET /v1/agents` | Registered agents: id, public key, last seen, online |
| `POST /v1/agents` `{"pubkey"}` | Registers an agent, as the broker's enrolment does (used by tests and tools) |

### Your own Gemini key

| Endpoint | Does |
| --- | --- |
| `GET /v1/session/key` | Whether this browser has a key: last 4 characters, added, last used, expiry, health. Never the key. |
| `POST /v1/session/key` `{"key"}` | Checks the key with one tiny Gemini request and, if it works, keeps it for this browser under a newly issued session id (cookie `pact_sid`, or `__Host-pact_sid` over HTTPS; `HttpOnly`, `SameSite=Strict`). 400 with `status` (`invalid`, `permission`, `quota_exhausted`, …) if it does not work. 429 after 5 checks from one browser, 10 from one address or 30 in total within 10 minutes. 403 if the feature is off (`PACT_USER_KEYS=off`). |
| `DELETE /v1/session/key` | Deletes this browser's key from the server and clears the cookie |

`GET /v1/status` includes an `llm` block for the calling browser: `available`, `source` (`yours`, `server` or `none`), `server` (the server key's health: `ok`, `quota_exhausted`, `invalid`, `permission`, `missing`, …), `yours` and `user_keys_enabled`. Drafts, tasks, scoreboard runs and red-team runs started by a browser with a key use that key; runs report it as `key_source`.

### Contracts and tasks

| Endpoint | Does |
| --- | --- |
| `POST /v1/contracts/draft` `{"instruction", "template"?}` | Gemini drafts a candidate (or `template: true` loads the pre-written one). Returns the contract, plain-language summary, ambiguities, warnings, and who drafted it. Nothing is signed. The instruction is limited to 4000 characters; a draft can be confirmed until its contract expires. |
| `POST /v1/contracts/{id}/confirm` `{"confirmed_by", "agent_id"?, "contract"?}` | Validates, binds to the agent, hashes and signs with the org key. Immutable afterwards. `contract` is the draft as edited by the signer: id, instruction and expiry still come from the server's draft, and the organisation's trust labels are put back if removed. |
| `GET /v1/contracts`, `GET /v1/contracts/{id}` | Confirmed contracts, signature verified on read |
| `POST /v1/capabilities` `{"contract_id"}` | Mints a capability for a confirmed contract |
| `POST /v1/capabilities/{id}/revoke` | Revokes it; later requests are denied with `capability_revoked` |
| `POST /v1/tasks` `{"contract_id", "kind"?: "live"\|"replay", "scenario"?, "variant"?, "reset"?, "max_steps"?}` | Mints a capability and hands the task to the agent. Returns `task_id`, `cap_id` and `assignment_id`. `kind` defaults to `live`, which needs a working Gemini key (409 otherwise). 409 while a scoreboard, replay or Break it run is going. |
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
| `GET /v1/evidence/export` | A self-contained bundle: records as stored, checkpoint, evidence key, org key and certificate. Verify it with `pact-core verify -org-pubkey ed25519:… FILE` (flags before the file), pinning the org key you trust; without a pinned key the check only shows the bundle is internally consistent. |

### Scenarios, scoreboard and red-team

| Endpoint | Does |
| --- | --- |
| `GET /v1/scenarios` | The seed scenarios and their variants |
| `GET /v1/oracles` | The targets' own state: secret reads, sink requests, pipeline hash, admin accounts, deployments |
| `POST /v1/demo/reset` `{"scenario"?, "variant"?}` | Resets the repository (optionally to a scenario's files) and clears the targets. 409 while a run is going. |
| `POST /v1/replay` `{"scenarios"?}` | Runs scripted scenarios with PACT on, checks every step and target, then resets. 409 while another run is going. |
| `POST /v1/scoreboard/runs` `{"agent"?, "quick"?, "supervisor"?, "max_steps"?, "timeout_seconds"?}` | Starts a run in the background (202). `agent` is `replay` (default) or `live`. `max_steps` 0 to 100 and `timeout_seconds` 0 to 1800, where 0 means the default. 409 if a run, or a task from *Run a task*, is still going. |
| `GET /v1/scoreboard/latest`, `GET /v1/scoreboard/runs`, `GET /v1/scoreboard/runs/{id}` | The current or latest run, the history, one stored run |
| `GET /v1/redteam` | The README a red-team run starts from, the two task wordings, the latest run |
| `POST /v1/redteam` `{"injection", "plain"?, "supervisor"?, "interactive"?}` | Plants the text (at most 8000 characters) in the untrusted README and runs the live agent in each configuration, in the background (202) |

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
