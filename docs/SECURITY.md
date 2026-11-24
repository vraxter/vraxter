# Vraxter Security & Isolation Architecture (`v1.0.0`)

Vraxter enforces **defense-in-depth, fail-closed security** across seven layers in pure Go (`CGO_ENABLED=0`).

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│                         VRAXTER SHIELD (v1.0.0)                              │
├──────────────────┬───────────────────────────────────────────────────────────┤
│ Layer            │ Enforced Controls                                         │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 1. Network       │ Deterministic airgap (loopback-only) / local_lan (RFC1918)│
│    Perimeter     │ / hybrid; in-memory DNS guard; IMDS block; DDIL timeouts. │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 2. Transport &   │ UDS (0600) on Unix, mandatory mTLS 1.3 on TCP listeners,  │
│    Identity      │ constant-time scoped tokens, verified Principal logging.  │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 3. Supply Chain  │ Ed25519 domain-separated signing (VRAXTER-SKILL-V1),      │
│    & PKI         │ trusted_signers/ store, verified SDK SHA-256 checksums.   │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 4. WASM Sandbox  │ Wazero WASI, mandatory CPU Fuel trap, memory/time caps,   │
│    & Containment │ QuotaFS, io.input.schema validation, os.OpenRoot.         │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 5. Governance &  │ HITL Decision Bridge, agent.allow_daemon gate,            │
│    Host Exec     │ HostExecGuard ($ disabled by default, SHA-256 allowlist). │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 6. Encryption &  │ Pure-Go AES-256-GCM DB columns/snapshots/sealed .db.enc,  │
│    Durability    │ exclusive vraxter.db.lock, WAL synchronous=FULL, 0600.    │
├──────────────────┼───────────────────────────────────────────────────────────┤
│ 7. Forensics     │ Append-only SQLite audit_ledger (abort triggers), SHA-256 │
│    & Telemetry   │ chain + Ed25519 signatures, offline verify, local OTel.   │
└──────────────────┴───────────────────────────────────────────────────────────┘
```

---

## 1. Network Isolation Modes

| Mode | Loopback (`127.0.0.1`, `::1`) | Private LAN (RFC 1918 / `.local`) | Cloud IMDS (`169.254.0.0/16`) | Public WAN / Public DNS |
| :--- | :---: | :---: | :---: | :---: |
| **`airgap`** *(Default)* | Allowed | **Blocked** *(unless explicitly whitelisted)* | **Blocked** | **Blocked** |
| **`local_lan`** | Allowed | Allowed | **Blocked** | **Blocked** |
| **`hybrid`** | Allowed | Allowed | **Blocked** | Allowed |

- **Strict Loopback `airgap`**: Under `airgap`, only loopback (`127.0.0.1`, `::1`, `localhost`) and explicit `whitelisted_ips` (IP or CIDR) are reachable. RFC 1918 intranet GPU servers require explicitly selecting `local_lan` or adding their exact CIDR to `whitelisted_ips`.
- **In-Memory DNS & Socket Interceptor (`AirgapDialer`)**: Rejects unauthorized hostnames and IPs before any DNS packet or TCP SYN leaves the process (`syscall.EPERM`), blocking DNS tunneling and TOCTOU DNS rebinding.
- **Anti-Downgrade Lockout**: Launching with `--airgap` or `VRAXTER_AIRGAP=1` permanently locks the process in `airgap`; any gRPC attempt to relax `isolation_mode` or `privacy_policy` fails with `codes.PermissionDenied`.
- **DDIL HTTP Timeouts**: All HTTP clients enforce strict dial, TLS handshake, response header, and idle timeouts.

---

## 2. Transport Security & Principal Attribution

- **Local IPC**: On Unix/POSIX, `vraxter server` / `vraxter daemon start` binds `unix://<AppDir>/vraxter.sock` with `0600` file permissions. On Windows, local IPC binds loopback `127.0.0.1:50051`.
- **Mandatory mTLS 1.3 for TCP**: Binding `--listen-tcp` requires `TLS 1.3` minimum with `RequireAndVerifyClientCert`. If custom CA/server certificates are not specified, Vraxter auto-generates an enclave-local PKI under `<AppDir>/pki/` with `0600` private keys.
- **Scoped Authentication**: Unary and streaming gRPC interceptors validate constant-time bearer tokens (`.daemon_key` or scoped `api_keys` created via `vraxter tokens create`) and enforce per-RPC scopes (`chat:write`, `skills:read`, `skills:write`, `config:write`, `host:exec`).
- **Non-Spoofable Audit Actor**: Every request derives a `Principal` from the authenticated mTLS peer certificate (CN/SAN) and/or verified `api_keys.id`. Client-supplied `SourceUser` strings are never trusted as authoritative audit actors.

---

## 3. Cryptographic Skill Supply Chain & SDK Integrity

- **Ed25519 Signing (`internal/security/ed25519_signing.go`)**: Signatures cover `"VRAXTER-SKILL-V1\n" + sha256(wasm) + "\n" + sha256(canonical_manifest_json)` and are verified against public keys registered in `<AppDir>/trusted_signers/`.
- **Zero App-Store Reputation Trust**: Trust tiers (`Tier1Official`, `Tier2CommunityVerified`) are granted solely by valid Ed25519 signatures from trusted signers or explicit local operator commands (`vraxter skill trust`). Unsigned skills default to `Tier3Unverified` and cannot access privileged mounts, network, or environment variables.
- **`require_signed_skills=true`**: Fails closed on unsigned or tampered skills at both registration and execution time, and disables runtime codegen (`allow_runtime_codegen=false`) by default.
- **SDK Checksums (`internal/services/toolchain.go`)**: Built-in checksums contain only verified official Go 1.22.5 and Zig 0.13.0 x86_64 SHA-256 digests. All other archives fail closed unless an operator explicitly supplies verified digests in `<sdkDir>/checksums.json`. In `airgap` mode, all SDK downloads are blocked.

---

## 4. WASM/WASI Sandbox, CPU Fuel, QuotaFS & Input Schema Enforcement

- **Mandatory CPU Fuel (`internal/skills/fuel.go`)**: Every `.wasm` module is instrumented at compile time with basic-block/loop fuel accounting; instrumentation failure fails closed. Infinite loops trap deterministically once `limits.max_cpu_fuel` is consumed without starving concurrent skills.
- **Memory, Binary & IO Bounds**: Configurable `limits.memory_mb` (Wazero page cap), `limits.max_runtime_ms`, concurrency semaphore, 50 MB `.wasm` load ceiling, SHA-256 compiled module cache, and 2 MB caps on `stdout`/`stderr`/`http_get` responses.
- **Runtime `io.input.schema` Validation (`internal/skills/input_schema.go`)**: Formal JSON Schema Draft-07 object schemas declared in `io.input.schema` are enforced before WASM instantiation; schema violations fail closed and log `POLICY_EVALUATED (DENY)`.
- **QuotaFS & Path Confinement**: Every WASI mount is wrapped in `QuotaFS` enforcing `allow_extensions`, `max_file_mb`, `max_total_mb`, `max_files`, and `allow_delete`. Untrusted parameter keys (`input_dir`, `_input_dir`, `output_dir`, `_output_dir`) are rejected. Native IO and sensory skills (`vraxter-read-file`, `vraxter-list-dir`, `vraxter-patch-code`, `vraxter-see`, `vraxter-hear`) are confined via `os.OpenRoot`.

---

## 5. HITL Governance & Guarded Host Exec (`HostExecGuard`)

- **HITL Gates**: Controlled via `require_skill_approval`, `agent.approval` (`always` / `on_risk`), `vraxter-patch-code`, `agent.allow_daemon`, and elevated/LLM host commands. Halts execution over the gRPC stream with `SKILL_APPROVAL_REQUEST` until confirmed by an operator.
- **`HostExecGuard` (`$`)**: Disabled by default (`host_exec.enabled=false`); never toggleable over gRPC. Requires local opt-in (`--enable-host-exec` or local `settings.json`), `host:exec` token scope, absolute-path + on-disk `SHA-256` match in `host_commands.json`, total blocking of shells and shell metacharacters, sanitized environment, network perimeter validation on arguments, and 2 MB output cap.

---

## 6. Pure-Go AES-256-GCM Encryption at Rest & Crash Durability

- **Master Key Custody**: `<AppDir>/db_master.key` (32 CSPRNG bytes, atomic `0600` creation).
- **Encrypted Columns & Backups**: When `encrypt_db_at_rest=true`, messages, RAG memories, sessions, tool schemas, and provider API keys are encrypted with AES-256-GCM using random 96-bit nonces (`vraxter-enc-v1:...`). Fail-closed: encryption failures abort writes instead of storing plaintext.
- **Sealed Database & Exclusive Lock**: Supports full-file AES-256-GCM `.db.enc` sealing and encrypted `autoBackup` snapshots. An exclusive OS file lock (`vraxter.db.lock`) prevents concurrent daemons or offline CLI commands from corrupting working database state.
- **Durability**: SQLite WAL mode with `PRAGMA synchronous=FULL`, `PRAGMA foreign_keys=ON`, `VACUUM INTO`, and atomic `0600` temp-file swaps.

---

## 7. Immutable Forensic Audit Ledger & Zero-Egress OTel

- **Append-Only Enforcement**: SQLite triggers (`BEFORE UPDATE`, `BEFORE DELETE`) unconditionally abort mutations (`RAISE(ABORT, 'audit_ledger is append-only')`).
- **Hash Chain & Ed25519 Signatures**: Each entry links `prev_hash` to `entry_hash = SHA-256(canonical_entry)` and attaches an Ed25519 signature from `<AppDir>/audit_identity.key` (`0600`). Sensitive fields (API keys, bearer tokens, private keys) are automatically redacted before persistence.
- **Offline Auditor Verification**: Export via `vraxter audit export --out ledger.jsonl` and verify on an independent offline machine with zero local `AppDir` side effects:
  ```bash
  vraxter audit verify --jsonl ledger.jsonl --pub audit_identity.pub
  ```
- **Local Zero-Egress OTel**: `internal/telemetry` runs an in-process OpenTelemetry tracer provider with zero outbound collectors in `airgap` mode (`NetworkEgressCount() == 0`).
