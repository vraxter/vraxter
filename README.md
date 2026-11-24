# VRAXTER

### **Sovereign Autonomous Execution Runtime (`v1.0.0`)**
*Deterministic, Zero-Egress Autonomous Execution for Critical Infrastructure, Defense, and Enterprise Enclaves.*

Vraxter is a single-binary, pure-Go (`CGO_ENABLED=0`) **Sovereign Autonomous Execution Runtime** engineered to run inside air-gapped networks, DDIL (Disconnected, Intermittent, Limited) tactical edge enclaves, and regulated enterprise environments. It separates probabilistic LLM reasoning from deterministic, capability-bounded **WebAssembly (WASI)** execution, backed by cryptographic supply-chain trust (Ed25519), AES-256-GCM encryption at rest, and an immutable hash-chained forensic audit ledger.

---

## Core Architecture & Security Guarantees (`v1.0.0`)

| Layer | Implemented Mechanism (`v1.0.0`) |
| :--- | :--- |
| **Transport & Auth** | Headless gRPC daemon over local `0600` Unix Domain Socket (`unix://<AppDir>/vraxter.sock`, or `127.0.0.1:50051` on Windows); mandatory **mTLS 1.3** (`--listen-tcp`) for network listeners; constant-time scoped API tokens enforced across unary and streaming RPCs; authenticated principal identity (mTLS CN/SAN + `api_keys.id`) logged in audit records. |
| **Network Isolation** | Deterministic socket- and resolver-level perimeter: `airgap` (default — **strict loopback-only** + explicit `whitelisted_ips`), `local_lan` (loopback + RFC 1918 intranet / `.local`), and `hybrid` (controlled egress). Blocks cloud IMDS (`169.254.0.0/16`, `fe80::/10`), public DNS leakage, and runtime security downgrades (`--airgap` / `VRAXTER_AIRGAP=1`). |
| **WASM/WASI Sandbox** | Wazero pure-Go runtime enforcing `skill-manifest/1` (`schemas/manifest.schema.json`), mandatory **WASM CPU Fuel** instrumentation (`limits.max_cpu_fuel`), bounded linear memory (`limits.memory_mb`), bounded execution timeout & concurrency semaphore, `QuotaFS` storage caps (`max_file_mb`, `max_total_mb`, `max_files`, `allow_extensions`, `allow_delete`), `os.OpenRoot` workspace confinement, and pre-execution JSON Schema Draft-07 validation of `io.input.schema`. |
| **Supply Chain (PKI)** | Domain-separated **Ed25519** signing (`VRAXTER-SKILL-V1`) over `.wasm` SHA-256 and canonical `skill-manifest/1` verified against local `trusted_signers/`. Trust is determined strictly by Ed25519 signatures and local policy (`require_signed_skills=true` blocks unsigned skills and disables runtime codegen by default). Verified SHA-256 checksum enforcement on SDK toolchains. |
| **Forensic Flight Recorder** | Append-only SQLite `audit_ledger` enforced by `BEFORE UPDATE`/`BEFORE DELETE` abort triggers, sequential SHA-256 hash chaining, Ed25519 daemon signatures (`audit_identity.key`, `0600`), automatic secret redaction, local zero-egress OpenTelemetry spans, and independent offline verification (`vraxter audit verify --jsonl --pub`). |
| **Encryption & Durability** | Pure-Go **AES-256-GCM** encryption at rest (`encrypt_db_at_rest=true`) for SQLite state columns, encrypted `.db.enc` snapshots, full-file sealed `.db.enc` mode, exclusive OS file lock (`vraxter.db.lock`), WAL with `synchronous=FULL`, `VACUUM INTO`, and atomic `0600` file writes. |
| **Deterministic Orchestration** | Native Tool Calling (`tools`/`tool_calls`) across OpenAI, Anthropic, Google, and Ollama; deterministic inference defaults (`temperature=0`, `seed`); per-provider Circuit Breakers with failover; Specialist Swarm delegation; Human-In-The-Loop (HITL) Decision Bridge; guarded `HostExecGuard` (`$` disabled by default, SHA-256 binary allowlist). |

---

## Reproducible Benchmarks & Dual-Use Offline Demos

### 7-Scenario Critical Benchmark Suite (`tests/benchmarks/`)
Run the full reproducible suite on your target hardware:

```bash
make bench
# or: CGO_ENABLED=0 go test -v -count=1 ./tests/benchmarks/...
```

Representative measurements captured on a 6-core `windows/amd64` host (`tests/benchmarks/benchmark_report.json`):
1. **Zero-Egress Air-Gap**: `20 / 20` egress attempts blocked (`100%`), `0` outbound packets, `0` OTel egress.
2. **WASM Cold/Warm Start vs. OS Subprocess**: Warm start `p50 = 1.99 ms` vs. OS subprocess `p50 = 17.40 ms` (**8.7x faster**).
3. **Deterministic DoS Containment**: Infinite CPU loop trapped at exact `500 / 500` fuel units (`10.5 ms`); 32 MB memory bomb trapped under 2 MB cap; `12 / 12` concurrent legitimate skills unaffected.
4. **Execution Determinism**: `50 / 50` runs produced 100% identical output, WASM, manifest, and audit hashes.
5. **Crash-Safety Recovery**: `25 / 25` killed write cycles with `0` corrupted atomic files, `0` orphan temps, `25 / 25` SQLite integrity checks passed, `5 / 5` sealed `.db.enc` cycles verified.
6. **Offline Skill Pipeline**: Compile + static verify + Ed25519 sign + register + first sandbox exec completed end-to-end offline in `3.31 s`.
7. **Immutable Audit Overhead**: `1,000` hash-chained Ed25519-signed SQLite events written at `p50 = 0.52 ms/event` (`1,226 ops/s`) and verified offline at `14,162 events/s`.

### Dual-Use 100% Offline Demos (`demos/`)
Execute both end-to-end integration demos with zero internet access:

```bash
make demos
# or: CGO_ENABLED=0 go test -v -count=1 ./demos/...
```

- **Demo 1 — Aerospace / Industrial Air-Gap Enclave** ([demos/01-aerospace-telemetry-airgap/](demos/01-aerospace-telemetry-airgap/README.md)): Signed `vrx-telemetry-analyzer` WASI skill (`fs: ro`, `network: none`, bounded CPU fuel) analyzing turbine telemetry under strict `--airgap` + `require_signed_skills=true`, neutralizing embedded prompt injection and exporting an independently verifiable forensic JSONL ledger.
- **Demo 2 — Sovereign SOC Incident Triage & Containment** ([demos/02-sovereign-soc-triage/](demos/02-sovereign-soc-triage/README.md)): Signed `vrx-soc-triage` WASI skill operating in `local_lan` mode, extracting lateral-movement IOCs and drafting `nftables` / YARA rules behind a mandatory HITL approval gate and `HostExecGuard` enforcement.

---

## Getting Started

### Reproducible Pure-Go Build (`CGO_ENABLED=0`)

```bash
git clone https://github.com/Vraxter/vraxter.git
cd vraxter

# Standard pure-Go reproducible binary
make build

# Pure-Go build with Go native FIPS 140-3 crypto module
make build-fips

# Verify build metadata (read-only; creates zero files)
./bin/vraxter version
```

### Day-1 Enclave Operations

```bash
# 1. Inspect default enclave policy (read-only)
vraxter config list

# 2. Enforce strict airgap, signed skills, and AES-256-GCM DB encryption
vraxter config set isolation_mode airgap
vraxter config set require_signed_skills true
vraxter config set encrypt_db_at_rest true

# 3. Start/manage detached background daemon
vraxter daemon start --airgap --require-signed-skills
vraxter daemon status
vraxter daemon stop

# 4. Register a local inference provider (secrets via env var or hidden prompt)
vraxter providers add local-ollama --type ollama --url http://127.0.0.1:11434
# For authenticated endpoints (never expose API keys in shell history/ps):
# vraxter providers add enclave-vllm --type custom --url http://10.0.1.20:8000/v1 --key-env VLLM_API_KEY

# 5. Ed25519 skill supply-chain workflow (offline PKI)
vraxter skill keygen --name "enclave-release-root" --out ./keys
vraxter skill sign --wasm ./skill.wasm --manifest ./manifest.json --key ./keys/enclave-release-root.key
vraxter skill trust add --pub ./keys/enclave-release-root.pub --name "enclave-release-root" --tier 1
vraxter skill verify --wasm ./skill.wasm --manifest ./manifest.json --sig ./skill.sig --pub ./keys/enclave-release-root.pub
vraxter skills inject ./skill.wasm --manifest ./manifest.json

# 6. Independent offline forensic audit verification (zero local AppDir required)
vraxter audit export --out ./flight_recorder.jsonl
vraxter audit verify --jsonl ./flight_recorder.jsonl --pub ./audit_identity.pub
```

### Operator TUI Workstation
Launch `./bin/vraxter` (or `./bin/vraxter --airgap`) to open the Anodized Titanium & Burnished Copper terminal console with live isolation status (`[ AIRGAP LOCKED ]`, `[ LAN ONLY ]`, `[ HYBRID ]`), `/start` enclave initialization wizard (`airgap` / `local_lan` / `hybrid`), `@<specialist>` delegation, and the interactive HITL **Decision Bridge**.

---

## Documentation Index

- [**Security Policy**](SECURITY.md) & [**Vraxter Shield Deep Dive**](docs/SECURITY.md) — Threat model, transport, isolation, sandbox, cryptography, and disclosure.
- [**Architecture Guide**](docs/ARCHITECTURE.md) — Daemon internals, gRPC/UDS/mTLS control plane, orchestration pipeline, and audit flow.
- [**Enterprise Architecture**](docs/ENTERPRISE_ARCHITECTURE.md) — Executive CISO/CTO overview, risk controls, and operational deployment models.
- [**Skills Development Guide**](docs/SKILLS_GUIDE.md) — `skill-manifest/1` contract (`schemas/manifest.schema.json`), Ed25519 signing, CPU fuel, `QuotaFS`, and WASI authoring.
- [**TUI Workstation**](docs/TUI_WORKSTATION.md) — Operator console reference, slash commands, and Decision Bridge.
- [**Specialist Swarm**](docs/SPECIALISTS.md) — Sub-agent rosters and structured handoffs.
- [**VRX Protocol**](docs/VRX_PROTOCOL.md) — Neural prompt template directives and markers.
- [**Roadmap**](ROADMAP.md) — Delivered `v1.0.0` guarantees and planned `v1.1+` Enterprise/Defense milestones.
- [**Changelog**](CHANGELOG.md) & [**Third-Party Notices**](NOTICE).

---

## License & Commercial Licensing

Vraxter uses a dual-licensing model:

1. **Open Source (AGPL-3.0)**: Vraxter Community Edition is licensed under the [GNU Affero General Public License v3.0](LICENSE). All source code is publicly auditable; derivative works and network-deployed modifications must comply with AGPL-3.0.
2. **Commercial Enterprise License**: Organizations requiring proprietary modifications, closed-source integration, SLA-backed defense/enterprise support, or exemption from AGPL-3.0 copyleft terms may obtain a commercial license from **DARZ Industries**.

External contributions require signing the [Contributor License Agreement (CLA)](CONTRIBUTING.md). See [NOTICE](NOTICE) for third-party open-source attributions.

---

## Operational Disclaimer

Vraxter executes autonomous reasoning workflows and capability-bounded tools on local infrastructure. Operators are responsible for configuring least-privilege `skill-manifest/1` policies, maintaining Ed25519 `trusted_signers/` custody, enabling `require_signed_skills` / HITL gates where required, and verifying audit trails before deploying into production or safety-critical environments.
