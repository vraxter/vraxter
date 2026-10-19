# Vraxter Security & Isolation (Vraxter Shield)

Vraxter is designed for high-stakes engineering environments where code execution safety, data sovereignty, and deterministic perimeter isolation are non-negotiable. The **Vraxter Shield** model provides defense-in-depth across six distinct layers:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             VRAXTER SHIELD                                  │
├─────────────────┬───────────────────────────────────────────────────────────┤
│ Layer           │ Defensive Mechanisms                                      │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 1. Network      │ Deterministic Zero-Egress Air-Gap, in-memory DNS filters,  │
│    Perimeter    │ transport-level socket interceptor (syscall.EPERM).       │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 2. Runtime      │ Anti-downgrade lockout (--airgap flag, immutable env),    │
│    Lockout      │ dynamic security downgrade rejection.                     │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 3. WASM Sandbox │ Wazero WASI V1 isolation, storage pools, virtualized FS.  │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 4. Toolchains   │ Hermetic offline builds (GOPROXY=off), SDK download block.│
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 5. Governance   │ Human-In-The-Loop (HITL) Decision Bridge approval gates.  │
├─────────────────┼───────────────────────────────────────────────────────────┤
│ 6. Cryptography │ SHA-256 binary checksums, AES-256-GCM credential vault.   │
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

### In-Memory DNS & Exfiltration Defense
In `airgap` mode, Vraxter never resolves external domains through public DNS servers:
- **IP Literals**: Parsed in-memory (`net.ParseIP`). Private and loopback IPs pass immediately without DNS calls. Public IPs are rejected instantly.
- **Local Domains**: Single-label hosts (e.g., `ollama-rig`) and `.local`/`.lan` domains resolve through local mDNS or LAN DNS.
- **External WAN Domains**: Public FQDNs (e.g., `api.openai.com`, `attacker.com`) are blocked **in memory** before any lookup occurs. This completely prevents data exfiltration via DNS tunneling and eliminates Time-of-Check to Time-of-Use (TOCTOU) DNS rebinding attacks.

### Transport-Level Socket Interceptor
Vraxter injects a custom `AirgapDialer` into `http.DefaultTransport`:
- Any socket dial attempting to reach a public WAN IP or external domain is intercepted before packet transmission and fails immediately with `syscall.EPERM` (`operation not permitted`).

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

## 3. WASM Sandbox & Skill Manifest V1

Vraxter executes all compiled tools inside the **Wazero** WebAssembly runtime, implementing a capabilities-based zero-trust architecture:

- **No OS Syscalls**: The WASM binary operates in user space and cannot invoke host system calls.
- **Resource Constraints**: Hardened limits (default 32MB–128MB RAM, 10–60 second CPU timeout) prevent Denial-of-Service and run-away loops.
- **Skill Manifest V1 (`skill-manifest/1`)**: All skills must declare explicit storage pools in `capabilities.fs.mounts`:
  - `workspace`: Mounted root of the current project directory (read-only or read-write).
  - `input`: Read-only pool for incoming user attachments (e.g., restricted to `.txt`, `.md`).
  - `output`: Sandboxed destination for generated reports or artifacts.
  - `skill_data`: Persistent storage private to that specific skill.
  - `ephemeral`: In-memory temporary directory purged after execution.
- **Deterministic Clocks & Entropy**: Skills can declare monotonic-only clocks and deterministic random sources to prevent side-channel timing attacks.

---

## 4. Hermetic Offline Toolchains

When synthesizing new skills autonomously (`vraxter-coder`):
- **Go Compilation**: The compiler runs with `GOPROXY=off` and `GOSUMDB=off` to guarantee that no dependencies are fetched over the network.
- **Toolchain Manager Guard**: Background SDK auto-downloads (`SetupSDK`) are blocked when airgap is active, requiring SDKs to be pre-populated offline in `~/.vraxter/sdk`.
- **Dry-Run Verification Pipeline**: Every generated binary undergoes a sandboxed dry-run test execution before being signed and persisted to prevent corrupted binaries from entering the vault.

---

## 5. Human-In-The-Loop (HITL) Execution Gates

For strict operational governance:
- Setting `require_skill_approval: true` halts execution whenever a tool invocation is planned.
- The daemon emits a `SKILL_APPROVAL_REQUEST` event over the Connect RPC stream.
- In the TUI, the **Decision Bridge** renders an approval card showing the skill ID, sandbox perimeter (`PERIMETER: 🛡️ AIRGAP`), and invocation payload.
- Execution cannot proceed without explicit operator confirmation (`[Enter]` to Accept, `[Esc]` to Deny).

```bash
vraxter config set require_skill_approval true
```

---

## 6. Authentication, Scoped Tokens & Cryptography

- **Dual-Tier Authentication**:
  - **Workstation Root Access**: Local IPC endpoints authenticate using an auto-generated master key (`~/.vraxter/daemon.key`) with restricted `0600` POSIX permissions.
  - **Scoped External Tokens**: External clients (dashboards, SCADA, IoT) use granular tokens created with `vraxter tokens create <name> --scopes="chat:write,skills:read"` to eliminate lateral privilege escalation.
- **Cryptographic Verification**: Every skill binary is hashed with **SHA-256**. `vraxter skills inspect <id>` verifies the on-disk binary against the database hash prior to execution.
- **Encryption at Rest**: Provider API keys, credentials, and master configurations are encrypted using **AES-256-GCM** inside the local SQLite database.
- **Data Sovereignty**: Chat history, memory embeddings, and specialist logs remain 100% on-premises. Zero telemetry or analytics are ever collected.
