# Vraxter Roadmap

## Delivered in `v1.0.0` (Sovereign Execution Foundation)

`v1.0.0` establishes a self-contained, single-binary, pure-Go (`CGO_ENABLED=0`) **Sovereign Autonomous Execution Runtime**:

- **Zero-Egress Network Perimeter**: Strict loopback-only `airgap` mode, RFC 1918 `local_lan` intranet mode, `hybrid` mode, in-memory DNS guard, IMDS (`169.254.0.0/16`, `fe80::/10`) blocking, and anti-downgrade `--airgap` lockout.
- **Authenticated Control Plane**: Local `0600` Unix Domain Socket (loopback on Windows), mandatory mTLS 1.3 for TCP listeners (`--listen-tcp`), constant-time scoped API tokens across unary and streaming gRPC, and verified principal attribution in audit records.
- **Hardened WASM/WASI Sandbox**: Wazero execution with mandatory deterministic CPU Fuel instrumentation (`max_cpu_fuel`), bounded memory pages, `QuotaFS` storage quotas, `os.OpenRoot` workspace confinement, runtime `io.input.schema` JSON Schema validation, and `HostExecGuard` SHA-256 command allowlisting.
- **Cryptographic Supply Chain**: Ed25519 skill signing (`vraxter skill keygen|sign|verify|trust add`), canonical manifest hashing (`skill-manifest/1`), `require_signed_skills` fail-closed enforcement, and verified SHA-256 SDK toolchain integrity checks.
- **Forensic Flight Recorder & Local OTel**: Append-only SQLite `audit_ledger` (immutable SQLite triggers), SHA-256 hash chain + Ed25519 entry signatures, secret redaction, local zero-egress OpenTelemetry spans, and independent offline verification (`vraxter audit verify --jsonl --pub`).
- **Encryption at Rest & Crash Safety**: Pure-Go AES-256-GCM column/backup/sealed-DB encryption, exclusive `vraxter.db.lock` locking, `synchronous=FULL` WAL durability, and atomic `0600` file writes.
- **Deterministic Orchestration**: Native Tool Calling (`tools`/`tool_calls`), deterministic inference parameters (`temperature=0`, `seed`), per-provider Circuit Breakers, HITL Decision Bridge, 7 reproducible critical benchmarks (`tests/benchmarks/`), and 2 offline dual-use demos (`demos/`).

---

## Planned Roadmap (`v1.1+` — Enterprise & Defense Scale)

The following capabilities are **not** yet implemented in `v1.0.0` and represent the prioritized engineering roadmap for `v1.1` through `v2.0`:

### `v1.1` — Governance, Policy-as-Code & Hardware Root of Trust
- **HITL M-of-N Dual-Control Approvals**: Cryptographically signed multi-operator approval quorums (e.g., 2-of-3 Ed25519 operator signatures) for `critical` risk skills, production containment actions, and elevated host execution.
- **Policy-as-Code Engine (OPA / CEL)**: Declarative, auditable Rego/CEL policy bundles evaluated pre- and post-tool execution alongside `skill-manifest/1` capabilities.
- **Hardware Key Custody (TPM 2.0 / PKCS#11 / HSM)**: Seal `db_master.key`, `audit_identity.key`, and mTLS private keys inside TPM 2.0 or PKCS#11 hardware security modules so raw private key material never resides on host disk.

### `v1.2` — Information Flow Control & Prompt-Injection Hardening
- **Bell-LaPadula Multi-Level Security (MLS)**: Mandatory classification labels (`UNCLASSIFIED`, `CUI`, `SECRET`, `TOP SECRET`) on sessions, mounts, RAG memories, and skills with enforced *no read up / no write down* isolation.
- **CaMeL / Dual-LLM Capability & Taint Tracking**: Separate privileged planner and quarantined data-reader execution contexts with provenance-tagged values so untrusted external data cannot steer privileged tool arguments.

### `v1.3` — Validated Cryptography & High-Availability Enterprise Backends
- **FIPS 140-3 Compliance Validation**: Formal validation workflow and reproducible release artifacts built with Go's native `GOFIPS140=v1.0.0` cryptographic module and CMVP-aligned audit documentation.
- **PostgreSQL High-Availability (HA) Backend**: Enterprise persistence driver supporting replicated PostgreSQL clusters with row-level tenant isolation and append-only partitioned audit tables.

### `v2.0` — DDIL Fleet Management & Air-Gap Artifact Distribution
- **Signed Offline Fleet Bundles (`.vrx-bundle`)**: Content-addressed, Ed25519-signed tarballs packaging WASM skills, `skill-manifest/1` descriptors, OPA/CEL policies, and root CA rotations for sneakernet/optical-diode synchronization across disconnected tactical and industrial fleets.
- **Fleet Control Plane**: Multi-node enclave enrollment, remote attestation, policy drift detection, and federated forensic audit aggregation.
