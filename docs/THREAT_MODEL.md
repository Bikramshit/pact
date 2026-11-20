# Threat model

What PACT MVP 0.1 defends, against whom, and where it stops.

## What is protected

| Asset | Protected against |
| --- | --- |
| Production secrets | Being read by the agent at all |
| External network | Data leaving through any channel the agent controls |
| Identity (accounts, roles) | Accounts the user never asked for |
| Deploy and CI configuration | Changes made after the agent has read untrusted content |
| The decision record | Being edited, reordered or cut short without detection |

## Who is trusted

| Component | Trust | Why |
| --- | --- | --- |
| `pact-core` (broker, policy, keys, evidence) | Trusted | It holds the credentials and makes every decision. Kept to one small Go service. |
| Host and container runtime | Trusted | Network isolation is enforced by Docker. |
| The user who confirms a contract | Trusted | Their confirmation is what gets signed. |
| A judge who adds their own Gemini key | Trusted with their key only | Their key is used for their runs only; it grants nothing else. |
| Gemini (drafting and the agent's model) | Untrusted | Advisory only: never holds a key, never decides. Its drafts are validated and need confirmation. |
| The agent | Untrusted | Assumed fully compromised or misaligned. |
| Repository content, tool output, network responses | Untrusted | Attacker-controllable text. |
| Test runner | Untrusted | It executes code the agent wrote. |

## Attacks and what stops them

| Attack | Stopped by | Tested by |
| --- | --- | --- |
| Injected text makes the agent read a secret | Contract denies `secret:*`; the request never reaches the store | S2 |
| Injected text makes the agent send data out | Contract denies `net:*`; the agent has no route of its own | S3, S5 |
| Injected text makes the agent create an admin | Contract denies `iam:*` | S4 |
| Injected text makes the agent do something the contract allows (edit the deploy pipeline) | Taint-gating: held for a human once untrusted content has been read | S6 |
| Agent reads data it is allowed to read, then sends it to a host it is allowed to reach | Exfiltration gate: outbound requests are held once confidential data has been read | S9 |
| Agent calls targets directly, around the broker | Internal Docker networks: the agent's only peer is the broker | S5, `make bypass-test` |
| Agent reaches the control plane to approve its own hold or mint a capability | The control-plane listener refuses connections arriving on the agent network | `make bypass-test` |
| Agent edits its capability to widen scope | Issuer signature over the exact token bytes | T1 |
| Agent or a network observer replays a request, under the same or another capability | Per-request signature, 60-second window, nonce single-use per agent key | T2, broker tests |
| Stolen capability used by another party | Every request must be signed by the agent key the capability is bound to | broker tests |
| Agent labels a secret read as a file read | The broker derives the resource from the tool and arguments itself | broker tests |
| Agent writes test code that calls the secret store | The test runner sits on its own network with no route to targets | `make bypass-test` |
| Agent uses the model provider as a way out (web search, URL fetch, file URIs) | The model proxy refuses provider-side tools and `fileData` at any depth, refuses repeated keys, and forwards its own re-encoding of what it checked | broker tests |
| Model drafts an over-broad contract | Validation against the resource catalogue, baseline denials added by code, human confirmation, warnings on risky grants | API tests |
| Model drafts a contract that exempts actions from the gates (`taint_tolerant`) | The organisation's taint-tolerant list replaces the model's; each dropped proposal is shown as a warning | contract tests |
| A held action is approved after its capability was revoked or expired | Validity is checked again after the decision; the action is not executed | broker tests |
| Someone with database access edits a decision record | Hash chain and signatures: verification names the record | T3, `make tamper` |
| Someone deletes the newest records | Signed checkpoint kept outside the database; `pact-core` refuses to append until the records are back, so the next decision cannot paper over the gap | `make truncate`, evidence tests |
| Someone appends text to a stored record, or repeats a key in it | Verification requires the exact canonical text that was signed | evidence tests |
| Logging fails, so an action goes unrecorded | Fail closed: no record, no result for the agent; the record is written even if the agent hangs up. If the database fails after an action with side effects ran, only `pact-core`'s log has it. | broker tests |
| Another website acts through a judge's signed-in browser: approves a hold, resets the demo, adds or removes a key | Every state-changing control-plane request needs the `X-PACT-Client` header, which a browser will not add cross-site; `Sec-Fetch-Site: cross-site` is refused; the dashboard cannot be framed | API tests |
| A web page rebinds its own DNS name to the control plane, so its scripts become same-origin | The control plane answers only to known host names (localhost, IP addresses, `*.sslip.io`, `*.nip.io`, `PACT_ALLOWED_HOSTS`) | API tests |
| A judge's own Gemini key leaks, or is used by someone else | Held encrypted in memory only, for one browser (`HttpOnly`, `SameSite=Strict` cookie, `__Host-` prefixed over HTTPS), referenced elsewhere by a random handle; never sent to the agent or back to any browser; deleted after 2 hours unused | vault and API tests, including a scan of the store, evidence, events, logs and assignments for the key |
| Someone plants a session cookie in a judge's browser, then uses the key the judge adds (session fixation) | A key is always stored under a fresh session id issued at that moment | API tests |
| Test code the agent wrote leaves something behind for later sessions | Each test run gets a fresh, empty home and temp directory, runs without user site-packages, and every process it started is killed | — |
| A reset or task changes the world while a scoreboard run is measuring it | Runs hold the world exclusively; resets and tasks get 409 meanwhile | API tests |

## What PACT does not do

- **Detect prompt injection.** It never judges whether text is malicious. Taint is "the agent read something untrusted", nothing finer.
- **Track taint per value.** Taint is per session, so a safe action can be held. The scoreboard counts these.
- **Treat test output as untrusted.** Tests can print text that came from a dependency; reading that output does not taint the session today.
- **Stop bad work inside the contract.** An allowed deploy of buggy code is allowed.
- **Verify effects.** It records what it executed, not whether the target ended up in the intended state.
- **Survive a compromised broker, key or host.** That voids the guarantee.

## Known gaps in MVP 0.1 (to close before a pilot)

- The control plane has no login. It relies on being bound to the host, and on Caddy's password when deployed. Deployed with `PACT_PUBLIC=1` there is no password at all: anyone with the link can approve or reject held actions, reset the demo, start runs on the server's Gemini key, and add or remove their own key. Approver names are free text.
- Agent enrolment on the broker port is unauthenticated. It hands out nothing usable without the agent's private key, but it should prove key possession.
- `pact-core` runs as root inside its container (read-only filesystem, all capabilities dropped).
- Pending holds live in memory and are lost if `pact-core` restarts; the request is then rejected by timeout.
- Signing keys are files on a volume unless Cloud KMS is configured.
- The passthrough and AI-supervisor modes used by the scoreboard ship in the same binary, behind `PACT_SCOREBOARD=1`. A production build should not contain them.
- Used nonces are never pruned.
- Agent registrations are kept in memory and never pruned; anyone who can reach the control plane can register agents.
- If the evidence database fails after an action with side effects has run, the result is withheld but the action is not undone, and only `pact-core`'s log records it. A write-ahead record would close this.
- The test runner's processes run as the same user as the runner itself, so test code can read the runner's start-up environment (its token for `pact-core`). The token only lets a caller ask the runner to run tests.
