# Vraxter Security & Isolation (Vraxter Shield)

Vraxter is designed for high-stakes engineering environments where code execution safety, data sovereignty, and deterministic perimeter isolation are non-negotiable. The **Vraxter Shield** model provides defense-in-depth across six distinct layers:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             VRAXTER SHIELD                                  │
├─────────────────┬───────────────────────────────────────────────────────────┤
│ Layer           │ Defensive Mechanisms                                      │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 1. Network      │ Deterministic Zero-Egress Air-Gap, in-memory DNS filters, │
│    Perimeter    │ IMDS link-local block, CIDR whitelist, DDIL HTTP timeouts.│
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 2. Runtime      │ Anti-downgrade lockout (--airgap flag, immutable env),    │
│    Lockout      │ dynamic security downgrade rejection.                     │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 3. WASM Sandbox │ Wazero WASI V1, Tier3Unverified fail-closed default,      │
│    & Host Exec  │ 50MB binary / 2MB IO caps, os.OpenRoot, HostExecGuard ($).│
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 4. Toolchains   │ Hermetic offline builds (GOPROXY=off), randomized tmpdirs,│
│                 │ ZipSlip/TarSlip guards, SDK download block in air-gap.    │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 5. Governance   │ Human-In-The-Loop (HITL) Decision Bridge approval gates   │
│                 │ for skills, plans, and elevated/LLM host commands.        │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 6. Auth, Crypto │ Unary+streaming scoped tokens, SHA-256 binary integrity,  │
│    & Durability │ AES-256-GCM API key vault, SQLite FULL sync + VACUUM INTO.│
└─────────────────┴───────────────────────────────────────────────────────────┘
```

---

## 1. Deterministic Air-Gap Shield & Isolation Modes

Vraxter enforces network boundaries through **`isolation_mode`**. Unlike conventional security tools that ping external endpoints to test connectivity, Vraxter uses **deterministic socket- and resolver-level denial**: zero network packets can leave the host perimeter towards the public internet.

### Isolation Modes Matrix

| Mode | WAN Egress | Local LAN (RFC 1918) | Loopback | Cloud LLMs | Use Case |
| :--- | :---: | :---: | :---: | :---: | :--- |
| **`airgap`** *(Default)* | ❌ Blocked | ✅ Allowed (`192.168.x`, `10.x`, `.local`) | ✅ Allowed | ❌ Disabled | Enclaves, air-gapped rigs, defense, healthcare |
| **`local_lan`** | ❌ Blocked | ✅ Allowed | ✅ Allowed | ❌ Disabled | Corporate intranet with local vLLM/Ollama servers |
| **`hybrid`** | ✅ Allowed | ✅ Allowed | ✅ Allowed | ✅ Enabled | Standard developer mode with cloud providers |

> [!NOTE]
> **Internal Private LAN Connectivity in Air-Gap**: An air-gap perimeter isolates Vraxter from the public WAN/Internet, but does **not** prevent communication within your trusted private intranet. Operators can connect to dedicated GPU clusters running Ollama, vLLM, or llama.cpp on the local network (e.g. `http://192.168.1.50:11434` or `http://gpu-box.local:11434`).

### In-Memory DNS, IMDS & Exfiltration Defense
In `airgap` and `local_lan` modes, Vraxter never resolves external domains through public DNS servers:
- **IP Literals**: Parsed in-memory (`net.ParseIP`). Private (RFC 1918) and loopback IPs pass immediately without DNS calls. Public WAN IPs are rejected instantly (unless explicitly permitted via `whitelisted_ips`, which supports exact IPs and CIDR prefixes via `net.ParseCIDR`).
- **Cloud Metadata (IMDS) Protection**: Link-local unicast ranges (`169.254.0.0/16` and `fe80::/10` — AWS/GCP/Azure IMDS) are **blocked by default** in restricted modes unless explicitly whitelisted.
- **Local Domains**: Single-label hosts (e.g., `ollama-rig`) and `.local`/`.lan` domains resolve through local mDNS or LAN DNS.
- **External WAN Domains**: Public FQDNs (e.g., `api.openai.com`, `attacker.com`) are blocked **in memory** before any lookup occurs. This prevents data exfiltration via DNS tunneling and eliminates Time-of-Check to Time-of-Use (TOCTOU) DNS rebinding attacks.

### Transport-Level Socket Interceptor & DDIL Timeouts
Vraxter injects a custom `AirgapDialer` and DDIL-hardened transport into `http.DefaultTransport` and all LLM/downloader clients:
- Any socket dial attempting to reach a public WAN IP or external domain in restricted modes is intercepted before packet transmission and fails immediately with `syscall.EPERM` (`operation not permitted`).
- All HTTP clients enforce strict DDIL timeouts (`10s` connect/TLS handshake, `30s` response header, `90s` idle connection), and the local HTTP server (`:8080`) enforces `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`.

---

## 2. Anti-Downgrade & Perimeter Lockout

To prevent malicious scripts, untrusted prompts, or misconfigured API clients from disabling security at runtime:
1. **Immutable CLI Flag & Environment**: Starting Vraxter with `--airgap` or setting `VRAXTER_AIRGAP=true` permanently locks the perimeter. Any gRPC or CLI command attempting to alter `isolation_mode` or `privacy_policy` is rejected with `codes.PermissionDenied`.
2. **Dynamic Runtime Downgrade Prevention**: If Vraxter is running in `airgap` mode, the engine refuses any dynamic downgrade to `hybrid` mode. Lowering security requires stopping the process and performing an administrative restart.

```bash
# Launch workstation locked in immutable airgap mode:
vraxter --airgap

# Inspect perimeter status:
vraxter config list
```

---

## 3. WASM Sandbox, Containment & Skill Manifest V1

Vraxter executes all compiled tools inside the **Wazero** WebAssembly runtime, implementing a capabilities-based, fail-closed architecture:

- **Fail-Closed Trust Tier**: All externally injected or dynamically synthesized skills default to `Tier3Unverified`. Any privileged capability (filesystem mounts, network, env) is blocked until explicitly trusted (`vraxter skills trust <id>`).
- **No OS Syscalls & Bounded Resources**: WASM binaries operate in user space with a 128 MB Wazero memory limit, 50 MB maximum `.wasm` binary load/hash cap, configurable execution timeouts (10–60s), and 2 MB hard caps on `stdout`, `stderr`, file outputs, and `http_get` responses (with redirect destination re-validation).
- **SHA-256 Module Cache**: Compiled WASM modules are indexed by SHA-256 digest of the bytes read from disk; modified binaries automatically evict and close stale compiled entries.
- **Skill Manifest V1 (`skill-manifest/1`)**: Skills declare explicit virtualized storage pools in `capabilities.fs.mounts`:
  - `workspace`: Mounted root of the current project directory (read-only or read-write).
  - `input`: Read-only pool for incoming user attachments.
  - `output`: Sandboxed destination for generated reports or artifacts.
  - `skill_data`: Persistent storage private to that specific skill.
  - `ephemeral`: Temporary directory purged after execution.
  - `host`: **Disabled by default** and **strictly forbidden in `airgap` mode**.
- **Path Traversal & Native Tool Confinement**: Mount `subpath`, guest paths, and `io.output.path` are statically validated against `..`, symlinks, and null bytes; `io.output.path` reads and native workspace IO skills (`vraxter-read-file`, `vraxter-list-dir`, `vraxter-patch-code`) are confined using Go's `os.OpenRoot`. Archive extraction (`ExtractTarGz`/`ExtractZip`) blocks ZipSlip/TarSlip traversal and symlink escapes.
- **Deterministic Clocks & Entropy**: Skills can declare monotonic-only clocks and optional random sources (`crypto/rand` disabled unless declared).

---

## 4. Hermetic Offline Toolchains

When synthesizing new skills autonomously (`vraxter-coder`):
- **Go Compilation**: The compiler runs with `GOPROXY=off` and `GOSUMDB=off` inside randomized `0700` temporary build directories (`os.MkdirTemp`) to prevent temp-dir collisions and dependency fetches over the network.
- **Toolchain Manager Guard**: Background SDK auto-downloads (`SetupSDK`) are blocked when airgap is active, requiring SDKs to be pre-populated offline in `~/.vraxter/sdk`. Downloads use atomic temp-file replacement and DDIL HTTP timeouts.
- **Dry-Run Verification Pipeline**: Every generated binary undergoes a zero-capability sandboxed dry-run test execution before its SHA-256 checksum is persisted. Failed binaries and manifests are immediately purged.

---

## 5. Human-In-The-Loop (HITL) Gates & Guarded Host Exec (`$`)

### HITL Skill & Plan Approvals
For strict operational governance:
- Setting `require_skill_approval: true` halts execution whenever a tool invocation is planned.
- The daemon emits a `SKILL_APPROVAL_REQUEST` event over the gRPC stream.
- In the TUI, the **Decision Bridge** renders an approval card showing the skill ID, active sandbox perimeter, and invocation payload.
- Execution cannot proceed without explicit operator confirmation (`[Enter]` to Accept, `[Esc]` to Deny).

```bash
vraxter config set require_skill_approval true
```

### Guarded Host Command Execution (`HostExecGuard`)
Direct local host command execution (`$ <cmd> [args...]`) is hardened under Zero-Trust rules:
- **Disabled by Default**: `host_exec.enabled` defaults to `false` and can only be enabled via local CLI flag (`vraxter --enable-host-exec`) or local config — never modifiable over RPC.
- **Explicit Scope**: Requires the `host:exec` scope on both unary and streaming RPC tokens.
- **SHA-256 Binary Allowlist**: Only commands explicitly listed with absolute paths and matching on-disk `SHA-256` digests in `~/.config/vraxter/host_commands.json` can execute.
- **No Shell / No Injection**: Shell binaries (`sh`, `bash`, `cmd`, `powershell`, etc.) and shell metacharacters (`;`, `|`, `&`, `` ` ``, `$()`, redirections) are unconditionally rejected. Commands run via direct `exec.CommandContext` with a minimal sanitized environment, strict timeouts, and a 2 MB `stdout`/`stderr` cap.
- **Risk Classification & HITL**: Each allowlisted entry is classified as `read_only` or `elevated`. Commands marked `elevated` — or **any** host command suggested by an LLM/specialist — always require explicit HITL approval (`!APPROVE_HOST_CMD`).
- **Network Destination Validation**: Arguments targeting IPs or hostnames in network commands are checked against `utils.IsAllowedNetworkWithMode` to enforce `--airgap` and `local_lan` boundaries.

---

## 6. Authentication, Scoped Tokens, Durability & Cryptography

- **Dual-Tier Authentication & Streaming Scopes**:
  - **Workstation Root Access**: Local IPC endpoints authenticate using an auto-generated master key (`~/.vraxter/daemon.key`) with restricted `0600` POSIX permissions.
  - **Scoped External Tokens**: External clients use granular tokens created with `vraxter tokens create <name> --scopes="chat:write,skills:read"` (and `host:exec` only if explicitly required). Scopes are enforced on both unary and streaming RPCs via `wrappedServerStream`.
- **Cryptographic Verification**: Every skill binary is hashed with **SHA-256**. Both `vraxter skills inspect <id>` and `Runner.Execute` verify the on-disk binary against the recorded hash prior to execution. *(Ed25519 asymmetric manifest signing is planned for a future hardening phase.)*
- **Encryption at Rest (Current Scope)**: Provider API keys (`providers.api_key`) are encrypted at rest using **AES-256-GCM** with a `0600` master key. Runtime logs (`vraxter.log`), `.wasm` binaries, and `.manifest.json` files use `0600` permissions. *(Full SQLite database encryption via SQLCipher and UDS/mTLS transport hardening are planned for the next phase.)*
- **SQLite Durability & Crash Resilience**: SQLite runs in WAL mode with `PRAGMA synchronous=FULL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`, and transactional `VACUUM INTO` backups.
- **Data Sovereignty**: Chat history, memory, and specialist logs remain 100% on-premises. Zero telemetry or analytics are collected.
