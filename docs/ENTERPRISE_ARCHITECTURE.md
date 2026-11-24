# Vraxter: Sovereign Autonomous Execution Runtime (`v1.0.0`)
## Enterprise Architecture & Executive Overview

**Release:** `v1.0.0`  
**Document Classification:** Executive / Technical Architecture  
**Target Audience:** CISOs, Enterprise Architects, Defense & Critical Infrastructure Engineering Leads

---

## 1. Executive Summary

Vraxter is a **Sovereign Autonomous Execution Runtime** engineered in pure Go (`CGO_ENABLED=0`) for high-assurance organizations that require autonomous AI workflows without surrendering data sovereignty, execution determinism, or cryptographic auditability.

Unlike cloud-hosted copilots or unconstrained shell-executing agent frameworks, Vraxter operates as a **zero-egress, single-binary daemon** that treats the LLM strictly as an untrusted reasoning engine bounded by deterministic runtime controls:
- **Strict Network Isolation** (`airgap` loopback-only default, `local_lan` RFC 1918 intranet, `hybrid` controlled egress).
- **Capability-Bounded WASM/WASI Execution** (Wazero + CPU Fuel + Memory Caps + `QuotaFS` + JSON Schema input validation).
- **Cryptographic Supply Chain** (Ed25519 signed skills and verified SHA-256 SDK checksums).
- **Tamper-Evident Forensics** (append-only hash-chained and Ed25519-signed SQLite/JSONL flight recorder).
- **Encryption at Rest** (pure-Go AES-256-GCM for SQLite columns, encrypted backups, and sealed database mode).

---

## 2. Core Enterprise Pillars (`v1.0.0`)

### 2.1 Zero-Egress Data Sovereignty
- **`airgap` (Default)**: Drops all public WAN, private RFC 1918 LAN, public DNS, and cloud metadata (`169.254.0.0/16`, `fe80::/10`) dials in memory before packets reach the OS kernel. Only loopback (`127.0.0.1`, `::1`, `localhost`) and explicit `whitelisted_ips` are reachable.
- **`local_lan`**: Permits private RFC 1918 intranet endpoints (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `.local`) for dedicated on-premises GPU clusters (vLLM / Ollama / llama.cpp) while preserving 100% WAN egress blocking.
- **Anti-Downgrade Lockout**: Starting with `--airgap` or `VRAXTER_AIRGAP=1` makes the isolation boundary immutable for the lifetime of the process.

### 2.2 Authenticated Control Plane (UDS + mTLS 1.3)
- Local Unix/POSIX IPC uses a `0600` Unix Domain Socket (`vraxter.sock`) eliminating local TCP port exposure.
- Any network TCP listener (`--listen-tcp`) requires **mTLS 1.3** (`TLS 1.3` minimum, client certificate verification).
- Granular scoped tokens (`chat:write`, `skills:read`, `skills:write`, `config:write`, `host:exec`) are verified in constant time on both unary and streaming RPCs.
- All audit entries attribute actions to the verified transport **Principal** (mTLS CN/SAN + `api_keys.id`), preventing actor spoofing.

### 2.3 Deterministic WASM Sandbox & Ed25519 Supply Chain
- Every skill runs inside a pure-Go **Wazero** WASI sandbox constrained by `skill-manifest/1` (`schemas/manifest.schema.json`).
- **Ed25519 Verification**: Skills are signed with domain-separated signatures (`VRAXTER-SKILL-V1`) covering both the `.wasm` binary SHA-256 and canonicalized manifest JSON, verified against local `trusted_signers/`. Enabling `require_signed_skills=true` rejects unsigned artifacts at install and execution time and disables dynamic runtime codegen by default.
- **Resource & DoS Bounds**: Mandatory **WASM CPU Fuel** instrumentation (`max_cpu_fuel`) traps infinite loops deterministically; `memory_mb` caps linear memory; `QuotaFS` enforces per-mount byte/file/extension/delete quotas; `io.input.schema` validates tool parameters before instantiation.

### 2.4 Forensic Flight Recorder & Encryption at Rest
- Every request, policy decision (`ALLOW`/`DENY`), tool invocation, and response is persisted to an append-only SQLite `audit_ledger` protected by SQLite `BEFORE UPDATE`/`BEFORE DELETE` abort triggers, SHA-256 hash chaining, and Ed25519 daemon signatures (`audit_identity.key`).
- Independent auditors verify exported JSONL streams offline without touching the enclave host: `vraxter audit verify --jsonl <file> --pub <audit_identity.pub>`.
- Pure-Go **AES-256-GCM** (`encrypt_db_at_rest=true`, `db_master.key` `0600`) encrypts sensitive database columns, encrypted `.db.enc` snapshots, and full-file sealed database storage under an exclusive OS file lock (`vraxter.db.lock`).

---

## 3. Threat & Risk Mitigation Matrix

| Risk | Vraxter `v1.0.0` Control |
| :--- | :--- |
| **Prompt Injection -> Destructive Host Execution** | LLMs never get raw shell access. Tools execute in Wazero WASM (`Tier3Unverified` fail-closed default, `source: "host"` forbidden in airgap, `os.OpenRoot` confinement). `HostExecGuard` (`$`) is disabled by default, requires local opt-in + `host:exec` scope + SHA-256 allowlist + HITL approval. `vraxter-patch-code` and `agent.allow_daemon` require explicit HITL approval. |
| **Supply-Chain Tampering** | Ed25519 `.sig` verification over `.wasm` SHA-256 + canonical manifest JSON before install and before every execution; verified official SHA-256 digests for SDK toolchains (fail-closed on unverified archives). |
| **Resource Exhaustion / DoS** | Deterministic WASM CPU Fuel trap (`max_cpu_fuel`), bounded linear memory (`memory_mb`), bounded execution timeouts, concurrent execution semaphore, `QuotaFS` disk quotas, and 2 MB IO caps. |
| **Data Exfiltration / DNS Tunneling / IMDS** | In-memory DNS filter + custom `AirgapDialer` dropping unauthorized dials with `syscall.EPERM`, IMDS link-local block, redirect SSRF re-validation, `GOPROXY=off`/`GOSUMDB=off` hermetic builds, zero-egress local OTel spans. |
| **Lateral Privilege Escalation** | `0600` UDS / mandatory mTLS 1.3 TCP, constant-time scoped tokens across unary+streaming RPCs, immutable `host_exec.enabled` (never mutable over gRPC), verified Principal logging. |
| **Log Tampering / Repudiation** | Append-only SQLite triggers + SHA-256 chain + Ed25519 daemon signatures + offline `--jsonl --pub` cryptographic verification + secret redaction. |
| **State Exposure at Rest & Crash Corruption** | Pure-Go AES-256-GCM encryption at rest (`encrypt_db_at_rest=true`), sealed `.db.enc` snapshots, exclusive `vraxter.db.lock`, WAL `synchronous=FULL`, atomic `0600` file writes. |

---

## 4. Reference Operational Use Cases (Validated in `demos/`)

1. **Aerospace & Industrial Air-Gap Enclaves** (`demos/01-aerospace-telemetry-airgap/`): Deterministic analysis of high-frequency turbine/sensor telemetry in a zero-egress `--airgap` enclave using an Ed25519-signed read-only WASI analyzer with embedded prompt-injection quarantine and verifiable forensic ledger export.
2. **Sovereign SOC Incident Triage** (`demos/02-sovereign-soc-triage/`): Offline lateral-movement log triage inside a `local_lan` intranet enclave producing IOC reports and `nftables`/YARA containment drafts gated behind mandatory HITL operator confirmation and `HostExecGuard` SHA-256 allowlisting.
3. **Regulated Enterprise & Government Engineering**: Air-gapped codebase analysis, compliance auditing, and multi-model deterministic orchestration (`temperature=0`, `seed`, native tool calling, per-provider circuit breakers) with zero cloud telemetry.

---

## 5. Transparency: `v1.0.0` Scope vs. `v1.1+` Roadmap

All controls documented in Sections 1–4 above are **implemented, tested, and verified in `v1.0.0`** (`CGO_ENABLED=0`). Enterprise/Defense enhancements planned for `v1.1+`—including HITL M-of-N dual-control quorums, OPA/CEL policy-as-code, Bell-LaPadula MLS labels, CaMeL dual-LLM taint tracking, TPM 2.0 / PKCS#11 hardware key custody, formal FIPS 140-3 validation artifacts, PostgreSQL HA, and DDIL Fleet `.vrx-bundle` distribution—are tracked transparently in [ROADMAP.md](../ROADMAP.md).

---

## 6. Licensing

Vraxter Community Edition is open-source under **AGPL-3.0**. Commercial enterprise and defense licensing, closed-source embedding rights, and dedicated support SLAs are available through **DARZ Industries**.
