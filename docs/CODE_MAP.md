# Code map

Where everything lives, what to read first, and how it is tested.

## Repository layout

```
pact/
├── cmd/pact-core/            entry point: control plane :8080, broker :9090, `verify` subcommand
├── internal/                 the trusted core, one Go module
│   ├── broker/               the agent's only door: gateway, tool catalogue, holds, sessions
│   │   └── executors/        talk to targets with broker credentials; Gemini proxy and fallback
│   ├── policy/               policy.rego (OPA) and the native Go engine
│   ├── contract/             schema, validation, Gemini drafting, registry
│   ├── capability/           mint and verify capability tokens
│   ├── evidence/             hash chain, checkpoint, verify, export
│   ├── keys/                 Ed25519 keys, optional Cloud KMS
│   ├── canon/                canonical JSON (what gets hashed and signed)
│   ├── netguard/             refuses connections arriving on the wrong network
│   ├── scoreboard/           scenario runner, oracles, three-way comparison, red-team runs
│   ├── api/                  control-plane handlers, live event stream, static dashboard
│   ├── store/                PostgreSQL store (and in-memory for tests)
│   ├── scenario/             loads the seed scenarios
│   ├── workspace/            the broker-side copy of the repository the agent edits
│   ├── mocktargets/          deploy, secret store, IAM, attacker sink, stand-in model
│   ├── agentclient/          a Go agent speaking the same protocol, used by the tests
│   └── testkit/              a whole PACT stack in one process, for acceptance tests
├── agent/                    the untrusted agent: Python, Google Gen AI SDK
├── targets/                  test runner and the mock-target binary
├── web/                      dashboard: Next.js static export, served by pact-core
├── demo/
│   ├── web-app/              the sample application (with a seeded bug and a poisoned README)
│   └── scenarios/            S1–S10, HERO, T1, T2 and the README variants
├── deploy/                   Docker Compose: default, offline stand-in model, Cloud KMS
├── scripts/                  bypass test, tamper and truncate, CLI driver
└── docs/                     these documents
```

## Read these first

Read them in this order to follow one tool call from start to finish.

| File | Why |
| --- | --- |
| [`internal/broker/gateway.go`](../internal/broker/gateway.go) | `process()` is the whole decision path: headers, capability, signature, derive, policy, execute or deny, record. Holds and the AI-supervisor baseline are here too. |
| [`internal/broker/catalog.go`](../internal/broker/catalog.go) | How the broker derives `resource:op` from a tool call, and what the model proxy refuses |
| [`internal/policy/policy.rego`](../internal/policy/policy.rego) | The decision chain in 90 lines: deny, gate, approval, allow |
| [`internal/policy/native.go`](../internal/policy/native.go) | The same chain in Go, tested to match |
| [`internal/contract/contract.go`](../internal/contract/contract.go) | The contract, its validator, signing and verification |
| [`internal/contract/draft.go`](../internal/contract/draft.go) | How Gemini drafts a contract and why it cannot weaken it |
| [`internal/capability/capability.go`](../internal/capability/capability.go) | Capability tokens |
| [`internal/evidence/evidence.go`](../internal/evidence/evidence.go) | Appending, hashing, chaining, the checkpoint, verification |
| [`deploy/docker-compose.yml`](../deploy/docker-compose.yml) | The four networks: the trust boundary |
| [`agent/pact_agent/loop.py`](../agent/pact_agent/loop.py) | The live agent, and how its model calls are routed through the broker |

## Tech stack

| Layer | Choice | Why |
| --- | --- | --- |
| Trusted core | Go 1.26, one static binary | Standard-library Ed25519 and HTTP |
| Policy | OPA, embedded through its Go `rego` package | Deterministic and testable; no extra service |
| Signatures | Ed25519 | Small keys, fast, built into Go and Python |
| Storage | PostgreSQL 17 | Unique constraint for nonces, serialised appends for the chain |
| Agent | Python 3.12, Google Gen AI SDK | The quickest route to a Gemini function-calling loop |
| Model | Gemini, with fallbacks | Contract drafting and the live agent |
| Dashboard | Next.js 16, React 19, Tailwind 4, lucide icons | Exported as static files, so there is no second server |
| Runtime | Docker Compose | Real network isolation on a laptop |
| Keys (optional) | Google Cloud KMS | Org and issuer keys never leave Google's service |

## Tests

```sh
go test ./...          # all Go tests, about 30 seconds
make test              # plus agent tests, dashboard type-check and build
make replay            # acceptance scenarios against the running stack
make bypass-test       # network isolation, against the running stack
```

| Test file | What it proves |
| --- | --- |
| `internal/scoreboard/scenarios_test.go` | Every seed scenario gets its expected decisions with PACT on (both policy engines). The same attacks land with PACT off. The AI-supervisor baseline runs. Reviewer outages are excluded. |
| `internal/broker/broker_test.go` | Denied calls never reach targets. Labels cannot be spoofed. Paths cannot escape. Every protocol violation is denied. No unlogged actions. Taint holds, approve, reject, timeout. The exfiltration gate. The model proxy's limits. Capabilities are bound to their agent. |
| `internal/api/api_test.go` | The whole demo through the HTTP API. Gemini drafting is advisory (bad drafts are replaced, risky grants flagged). Red-team input validation. |
| `internal/policy/policy_test.go` | The decision table, glob semantics, determinism, and OPA and native agreement on 1,088 inputs |
| `internal/evidence/evidence_test.go` | Edits, added fields, deletions and wrong keys are all located |
| `internal/capability/capability_test.go` | Flipping any byte of a token fails verification |
| `internal/contract/contract_test.go` | Changing any field breaks the signature; the validator rejects unknown resources |
| `internal/keys/keys_test.go` | Cloud KMS signing against a fake KMS; the evidence-key certificate |
| `internal/broker/executors/executors_test.go` | Model retry, fallback, cooldown and rate-limit handling |
| `internal/netguard/netguard_test.go` | Which networks each port accepts |
| `agent/tests/test_signer.py` | The agent's signing string matches the broker's exactly |

The acceptance tests run a complete stack in one process (`internal/testkit`): the real broker, policy, evidence and mock targets over HTTP, and a Go agent that speaks the same wire protocol as the Python one. They need no Docker.

## Where the architecture document was changed on purpose

The README lists the places this implementation departs from "PACT MVP 0.1 — Implementation Architecture", and why. In short:

- the test runner has its own network
- the ports are separated in code as well as by publishing
- agent enrolment happens on the broker port
- `:` is the glob delimiter
- a mock IAM target was added
- PACT-off sessions are started per run and revoked when the run ends
- demo egress is pinned to the sink
- the model proxy is restricted
- a hold writes two records
