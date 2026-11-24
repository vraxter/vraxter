# Security Policy

## Supported Versions

| Version | Supported | Notes |
| :--- | :---: | :--- |
| `1.0.x` | Yes | Active security hardening and critical fixes |
| `< 1.0.0` | No | Pre-release development snapshots |

## Threat Model & Core Security Guarantees (`v1.0.0`)

Vraxter is engineered as a **Sovereign Autonomous Execution Runtime** for air-gapped, DDIL (Disconnected, Intermittent, Limited), enterprise, and critical-infrastructure enclaves. Every security boundary is enforced **fail-closed** in pure Go (`CGO_ENABLED=0`):

1. **Deterministic Network Perimeter (`airgap` / `local_lan` / `hybrid`)**:
   - `airgap` (default): strictly loopback-only (`127.0.0.1`, `::1`, `localhost`) plus explicit `whitelisted_ips`; all WAN, private RFC 1918 LAN, public DNS, and cloud metadata (`169.254.0.0/16`, `fe80::/10`) destinations are dropped in-memory before SYN packets reach the kernel.
   - `local_lan`: allows loopback and RFC 1918 private subnets / `.local` / `.lan` for on-premises GPU inference clusters while blocking all public internet egress.
   - `--airgap` / `VRAXTER_AIRGAP=1`: permanently locks the session perimeter against runtime API downgrades.
2. **Authenticated Local & Network Transport**:
   - Unix/POSIX local IPC binds a Unix Domain Socket (`vraxter.sock`) with `0600` permissions (loopback `127.0.0.1:50051` on Windows).
   - Any TCP listener (`--listen-tcp`) requires **mTLS 1.3** (`TLS 1.3` minimum, `RequireAndVerifyClientCert`).
   - Constant-time bearer key verification and per-RPC granular scopes (`chat:write`, `skills:read`, `skills:write`, `config:write`, `host:exec`, etc.) are enforced on both unary and streaming RPCs.
   - Audit attribution derives actor identity strictly from authenticated transport principals (mTLS CN/SAN and `api_keys.id`), ignoring client-spoofed identity fields.
3. **Capability-Bounded WASM/WASI Sandbox (Wazero)**:
   - Ed25519 cryptographic signing and verification against local `trusted_signers/` (`require_signed_skills=true` fails closed on unsigned binaries and disables runtime codegen by default).
   - Trust is determined exclusively by Ed25519 signatures and explicit local operator policy (no external reputation or app-store scoring).
   - Mandatory deterministic **WASM CPU Fuel** instrumentation (`max_cpu_fuel`), bounded linear memory pages (`memory_mb`), bounded execution timeouts, concurrent execution semaphore, `QuotaFS` (`max_file_mb`, `max_total_mb`, `max_files`, `allow_extensions`, `allow_delete`), `io.input.schema` JSON Schema Draft-07 validation, and `os.OpenRoot` confinement.
   - `HostExecGuard` (`$`): disabled by default, toggleable only locally (`--enable-host-exec` or local `settings.json`), requires `host:exec` scope, SHA-256 binary allowlist (`host_commands.json`), shell/metacharacter rejection, and HITL approval for elevated/LLM commands.
4. **Encryption at Rest & Crash Durability**:
   - Pure-Go **AES-256-GCM** encryption at rest (`encrypt_db_at_rest=true`) for SQLite state columns, encrypted `.db.enc` snapshots, full-file sealed `.db.enc` mode, and exclusive `vraxter.db.lock` locking to prevent concurrent corruption.
   - WAL mode with `PRAGMA synchronous=FULL`, atomic file replacement (`0600`), and `VACUUM INTO` backups.
5. **Immutable Forensic Audit Ledger (Flight Recorder)**:
   - Append-only SQLite `audit_ledger` enforced by `BEFORE UPDATE` / `BEFORE DELETE` abort triggers.
   - Sequential SHA-256 hash chain signed by the daemon's Ed25519 identity key (`audit_identity.key`, `0600`) with automatic secret redaction and local zero-egress OpenTelemetry spans.
   - Independent offline verification via `vraxter audit verify --jsonl <file> --pub <audit_identity.pub>`.

For full technical details, see [docs/SECURITY.md](docs/SECURITY.md).

## Reporting a Vulnerability

Do **not** disclose security vulnerabilities in public GitHub issues or pull requests.

Please report suspected vulnerabilities privately to the Vraxter / DARZ Industries security team with:
- Affected component, version (`vraxter version`), and isolation mode (`airgap`, `local_lan`, or `hybrid`).
- Minimal reproduction steps or proof-of-concept test case.
- Expected fail-closed behavior vs. observed behavior.

We acknowledge security reports within **48 hours** and coordinate remediation and disclosure timelines with the reporter.
