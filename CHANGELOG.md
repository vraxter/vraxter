# Changelog

All notable changes to the **Vraxter Sovereign Autonomous Execution Runtime** are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [1.0.0] - 2026-11-23

Initial production release of the pure-Go (`CGO_ENABLED=0`) Sovereign Autonomous Execution Runtime.

### Security & Transport Hardening
- **Local UDS (`0600`) & Mandatory mTLS 1.3**: Unix/POSIX daemons listen on `unix://<AppDir>/vraxter.sock` (`0600`); Windows defaults to loopback `127.0.0.1:50051`. Any `--listen-tcp` binding requires mTLS 1.3 (`TLS 1.3` minimum, client certificate verification) with automatic local PKI generation (`ca.crt`, `server.crt/key`, `client.crt/key`).
- **Strict Network Isolation Modes**: Enforced `airgap` (strict loopback-only + explicit `whitelisted_ips`, zero WAN, zero RFC 1918 LAN), `local_lan` (loopback + RFC 1918 private intranet only), and `hybrid` (controlled egress), with in-memory DNS filtering, IMDS blocking (`169.254.0.0/16`, `fe80::/10`), and immutable `--airgap` / `VRAXTER_AIRGAP=1` anti-downgrade lockout.
- **Authenticated Principal Attribution**: gRPC authentication verifies constant-time bearer tokens, granular unary/streaming scopes, and mTLS peer identities (CN/SAN + `api_keys.id`), recording verified actor principals in `audit_ledger` while ignoring spoofed `SourceUser` fields.
- **Guarded Host Exec (`HostExecGuard`)**: Disabled by default (`host_exec.enabled=false`, never mutable over gRPC); requires `host:exec` scope, SHA-256 binary allowlist (`host_commands.json`), shell/metacharacter rejection, and HITL approval for elevated/LLM commands.

### WASM/WASI Sandbox & Supply Chain
- **Ed25519 Skill Signing & `trusted_signers/`**: Domain-separated (`VRAXTER-SKILL-V1`) Ed25519 signing over WASM SHA-256 and canonicalized `skill-manifest/1`, offline CLI PKI (`vraxter skill keygen|sign|verify|trust add`), fail-closed `require_signed_skills=true`, and removal of app-store reputation scoring.
- **Deterministic CPU Fuel & QuotaFS**: Mandatory WASM bytecode basic-block/loop fuel instrumentation (`limits.max_cpu_fuel`) trapping infinite loops deterministically, per-mount `QuotaFS` limits (`max_file_mb`, `max_total_mb`, `max_files`, `allow_extensions`, `allow_delete`), `os.OpenRoot` workspace confinement for native IO and sensory skills (`vraxter-see`/`vraxter-hear`), and `agent.allow_daemon` + HITL gating for background daemon tools.
- **Runtime Input Schema Validation**: Enforced JSON Schema Draft-07 validation of tool invocation `params` against `io.input.schema` prior to WASM instantiation, synchronized with `schemas/manifest.schema.json`.
- **Verified Toolchain Checksums**: Audited `ToolchainManager` SHA-256 digests to include only verified official Go 1.22.5 and Zig 0.13.0 x86_64 releases; unverified archives fail closed unless pinned locally via `<sdkDir>/checksums.json`.

### Forensic Audit Trail & Persistence
- **Immutable Forensic Flight Recorder**: Append-only SQLite `audit_ledger` protected by `BEFORE UPDATE`/`BEFORE DELETE` abort triggers, sequential SHA-256 hash chaining, Ed25519 daemon signatures (`audit_identity.key`), secret redaction, local zero-egress OpenTelemetry spans, and independent offline verification (`vraxter audit verify --jsonl <file> --pub <audit_identity.pub>`).
- **Pure-Go AES-256-GCM Encryption at Rest**: Column-level encryption for messages, memories, sessions, tool schemas, and provider credentials (`vraxter-enc-v1:`), encrypted `.db.enc` snapshots, full-file sealed `.db.enc` mode, and exclusive OS file lock (`vraxter.db.lock`) preventing concurrent DB corruption.

### Orchestration, Benchmarks & Dual-Use Demos
- **Native Tool Calling & Circuit Breakers**: Wired OpenAI, Anthropic, Google, and Ollama native tool schemas and structured tool calls (`EventTypeNativeToolCall`), deterministic inference defaults (`temperature=0`, `seed`), and per-provider circuit breakers with automatic failover.
- **7-Scenario Critical Benchmark Suite**: Added reproducible quantitative validation suite in `tests/benchmarks/` covering Zero-Egress Air-Gap, WASM Cold/Warm Start vs. OS Subprocess, Deterministic DoS Containment, Execution Determinism, Crash-Safety Recovery, Offline Skill Pipeline, and Immutable Audit Overhead.
- **Offline Dual-Use Demos**: Added self-contained E2E demos in `demos/01-aerospace-telemetry-airgap/` and `demos/02-sovereign-soc-triage/` with real compiled WASI skills, Ed25519 signatures, and automated verification tests.

### Removed / Cleaned Up
- Removed consumer/smart-home scaffolding (`internal/services/spatial.go`, `internal/env/`, `internal/api/websocket.go`, HTTP `:8080` server, `internal/features/*`, `github.com/gorilla/websocket`, and `crypto/md5` references).
- Replaced consumer onboarding with Enclave Isolation (`airgap` / `local_lan` / `hybrid`) and Operator Profile configuration; added `--key-env` and hidden terminal prompts to `vraxter providers add`; decoupled read-only CLI commands from daemon bootstrap to eliminate stray disk artifacts.
