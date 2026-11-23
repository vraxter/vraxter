# VRAXTER

### **The Sovereign Autonomous AI Engine**
*Engineering Autonomy. Standardizing Intelligence.*

> [!WARNING]
> Vraxter is still in development process, refinement and aligning
> specifications. We will likely to introduce major breaking
> changes prior to a stable release.
> Please feel free to contribute and create issues if you find some.

Vraxter is a **Sovereign Autonomous AI Engine** designed to bridge the gap between high-level LLM reasoning and native, deterministic system execution. It operates as a headless, highly-concurrent daemon that interprets intent, orchestrates specialized sub-agents, and automates workflows across both critical and non-critical environments—from corporate engineering workstations and tactical defense edge-nodes, to industrial facilities and smart home automation.

## Core Architecture

Vraxter is built on the **Sovereign-Agent Model**, ensuring that your proprietary data, logic, and execution stay strictly within your infrastructure. It is built to run anywhere, serving as the localized brain for complex physical and digital ecosystems.

### 1. Client-Agnostic gRPC Interface
Vraxter operates as a completely headless backend daemon exposing a **gRPC (HTTP/2)** API on `127.0.0.1:50051` (plus an experimental Connect RPC HTTP endpoint on `127.0.0.1:8080` currently serving the `EnvService` skeleton). It is uncoupled from any specific UI framework, allowing you to connect Thin CLI interfaces, Web Dashboards, IoT hardware, or SCADA systems.

### 2. Spatial Context Engine & Zone-Based Concurrency
Vraxter natively understands physical and digital spaces. It supports **Spatial Isolation**: whether deployed in a multi-room smart home, partitioned corporate office, or distinct industrial facility zones, Vraxter isolates concurrent conversation histories and side-effects based on physical location, allowing dynamic, concurrent orchestration across a wide network of users and sensors.

### 3. The Autonomous Execution Loop
Vraxter doesn't just generate text; it solves problems by iterating through an autonomous cycle:
- **Analyze & Plan**: Decomposes complex queries into actionable phases using its internal Planner.
- **Multimodal Senses**: Native Official Skills allow Vraxter to see images, hear audio, and synthesize speech targeting specific physical rooms.
- **WASM Skill Execution**: Runs highly performant, sandboxed tools compiled to WebAssembly (Go, Rust, Zig).

### 4. Specialist Swarm
Vraxter manages a roster of domain-specific sub-agents (e.g., Security Auditor, Frontend Architect) that can be delegated to for hyper-focused reasoning. The Supervisor engine orchestrates handoffs seamlessly.

### 5. Multi-Model Intelligence & Custom APIs
Stop being locked into a single provider. Vraxter features dynamic, priority-based routing across:
- **Cloud**: OpenAI, Anthropic, Google.
- **Enterprise Open-Source**: Native `custom` provider wrapper supporting OpenAI-API compliant servers like vLLM, LM Studio, or Text Generation Inference natively.
- **Failover**: Automatic fallback to secondary models if a provider is unreachable.


## 🚀 Getting Started

### Installation
Vraxter is distributed as a single high-performance binary.

```bash
# Clone the repository
git clone https://github.com/Vraxter/vraxter.git
cd vraxter

# Build the workstation
# Build the workstation
go build -o bin/vraxter ./cmd/vraxter
```

### The Onboarding Wizard
The first time you run Vraxter, or by typing `/start`, you will enter the **Premium Onboarding Flow**. This TUI-native wizard handles:
1. **User Profile**: Defining your expertise and preferred interaction style.
2. **Provider Setup**: Securely managing API keys and base URLs.
3. **Model Registration**: Configuring your reasoning engines and use-case priorities.

```bash
# Standard Launch
./bin/vraxter

# Or launch locked into 100% Zero-Egress Air-Gap mode
./bin/vraxter --airgap

# Once inside the TUI, type:
/start
```

### The Professional TUI Workstation
Vraxter features a high-fidelity terminal interface styled in an **Anodized Titanium & Burnished Copper** aesthetic. It includes a double-rail command deck with an **Ambient Isolation Capsule** (`[ 🛡️ AIRGAP LOCKED ]`, `[ 🌐 LAN ONLY ]`, `[ ⚡ HYBRID ]`), an interactive `/` command overlay, a `@` specialist autocomplete shelf for instant sub-agent delegation, and a semantic handoff mask that hides internal JSON protocol signals. Human-in-the-loop decisions are handled via an interactive **Decision Bridge**. See the [TUI Workstation Guide](docs/TUI_WORKSTATION.md) for more details.

## Technical Stack

| Component | Technology | Rationale |
| :--- | :--- | :--- |
| **Language** | Go (1.24+) | High-speed concurrency, `os.OpenRoot` confinement, and single-binary portability. |
| **Sandbox** | Wazero (WASI V1) | Hardened, capabilities-based WebAssembly sandbox without local dependencies. |
| **Persistence** | SQLite | Local-first relational storage in WAL mode with `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=5000`, and `VACUUM INTO` backups. |
| **TUI Interface** | Bubble Tea & Lip Gloss | Anodized Titanium & Burnished Copper terminal interface with double-rail command deck. |
| **Internal Comms** | gRPC (HTTP/2) + Connect RPC (Env skeleton) | Client-agnostic API layer using Protocol Buffers (`127.0.0.1:50051` gRPC primary, `127.0.0.1:8080` HTTP env service). |

## Command Reference

Vraxter features a sophisticated "Slash Command" system for workstation management:

| Command | Description |
| :--- | :--- |
| `/start` | Launches the unified onboarding/setup wizard (Profile ➔ Providers ➔ Models). |
| `/help` | Displays the interactive command documentation and keyboard shortcuts. |
| `/airgap`, `/isolation` | Displays live telemetry on network perimeter defense, socket dialer, and zero-egress status. |
| `/plan <query>` | Forces Vraxter to generate a structured implementation strategy using multi-phase `.vrx` planning. |
| `/specialists` | Manages the roster of domain-specific sub-agents (or delegate via `@<name>`). |
| `/skills` | Manages and synthesizes WASM capabilities (`/skills add <name> <spec>`). |
| `/providers` | Adds or modifies LLM provider configurations (`/providers setup` for wizard). |
| `/models` | Registers and prioritizes specific reasoning models (`/models setup` for wizard). |
| `/activate <id>` | Manually switches and locks the active model for the current session. |
| `/restore`, `/auto` | Restores automatic use-case driven model routing. |
| `/jobs` | Lists all background daemon jobs currently executing. |
| `/history` | Browses past conversation sessions in the local vault. |
| `/load <#>` | Replays a past conversation by its index number from `/history`. |
| `/session <id>` | Switches context to a specific topic or conversation ID. |
| `/clear` | Resets the current chat viewport while preserving memory. |
| `/factory-reset` | Restores Vraxter completely to factory defaults with automatic backup. |

### CLI Management Commands

- `vraxter --airgap`: Launch workstation or server locked into deterministic zero-egress airgap mode.
- `vraxter --enable-host-exec`: Explicitly enable guarded local host command execution (`$`) backed by `~/.config/vraxter/host_commands.json` (disabled by default; never toggleable over RPC).
- `vraxter config list`: Inspect global settings, active isolation mode (`airgap`, `local_lan`, `hybrid`), and perimeter lock status.
- `vraxter config set isolation_mode <airgap|local_lan|hybrid>`: Configure network isolation boundary.
- `vraxter config set require_skill_approval true`: Enforce Human-In-The-Loop (HITL) manual skill approvals for strict execution governance.
- `vraxter user <profile|setup|update>`: View or configure your local operator profile.
- `vraxter providers <id> discover`: Discover available models for a specific provider.
- `vraxter providers setup`: Interactive wizard to set up a new provider.
- `vraxter providers add <name> --type <type>`: Add a provider non-interactively.
- `vraxter skills inject <path.wasm|skill_dir|manifest.json>`: Inject local skill binaries or Manifest V1 packages.
- `vraxter skills inspect <id>`: Verify cryptographic SHA-256 hash of a skill binary against the vault.
- `vraxter tokens create <name> --scopes="chat:write,skills:read"`: Generate scoped API tokens for external clients (add `host:exec` only when explicitly granting `$` access).

## Full Documentation

For deep dives into Vraxter's internals, development guides, and security model, explore our complete documentation suite:

- [**Architecture Guide**](docs/ARCHITECTURE.md): Distributed systems and Connect RPC data flow.
- [**Enterprise Architecture**](docs/ENTERPRISE_ARCHITECTURE.md): Executive overview of the Sovereign Autonomous AI Engine.
- [**Implementations Guide**](docs/IMPLEMENTATIONS.md): Vraxter build tags, worlds, and execution capabilities.
- [**TUI Workstation**](docs/TUI_WORKSTATION.md): High-fidelity terminal interface and interactive overlays.
- [**VRX Protocol Spec**](docs/VRX_PROTOCOL.md): Mastery of neural prompt templating.
- [**Skills Development**](docs/SKILLS_GUIDE.md): Building and registering WASM-based tools.
- [**Specialist Swarm**](docs/SPECIALISTS.md): Managing autonomous sub-agents and delegation.
- [**Vraxter Shield**](docs/SECURITY.md): Security, isolation, and data sovereignty.



## 🛡️ Vraxter Shield (Security Architecture)

Vraxter implements a **Secure-by-Design** philosophy for Enterprise and DDIL environments:
- **Deterministic Zero-Egress Air-Gap & IMDS Blocking**: Operates on a pure socket- and resolver-level isolation model without external ping checks. In-memory IP inspection blocks public domains before emitting DNS queries, blocks cloud metadata / link-local endpoints (`169.254.0.0/16`, `fe80::/10`) by default in restricted modes, supports CIDR prefixes in `whitelisted_ips`, and applies strict DDIL HTTP transport timeouts.
- **Internal Private LAN Supported**: The air-gap perimeter isolates Vraxter from the public WAN while permitting trusted communication within your private corporate intranet or home lab (RFC 1918: `192.168.x.x`, `10.x.x.x`, `172.16.x.x`, and `.local` mDNS). Operators can seamlessly connect to dedicated GPU clusters running Ollama, vLLM, or llama.cpp on the local network.
- **Anti-Downgrade & Perimeter Lockout**: When executed with `--airgap` or `VRAXTER_AIRGAP=true`, the isolation perimeter becomes immutable. Any runtime attempts to downgrade security to hybrid mode over the API are rejected, requiring an administrative daemon restart.
- **Guarded Host Exec (`HostExecGuard`)**: Direct host command execution (`$`) is **disabled by default** and can only be enabled locally via `--enable-host-exec` or local config (never over RPC). Execution requires the explicit `host:exec` token scope, an absolute-path allowlist + on-disk `SHA-256` verification in `~/.config/vraxter/host_commands.json`, total blocking of shells and shell metacharacters, sanitized environment variables, a 2 MB output cap, network destination checks against the active isolation perimeter, and mandatory HITL approval for `elevated` or LLM-originated commands.
- **Fail-Closed WASM Manifest V1 Sandbox**: Tools run inside Wazero using the `skill-manifest/1` specification. External and synthesized skills default to `Tier3Unverified`, `source: "host"` mounts are disabled by default (and forbidden in `airgap`), path traversal and ZipSlip/TarSlip are blocked, `.wasm` binaries are capped at 50 MB and cached by SHA-256, `stdout`/`stderr`/`http_get` payloads are capped at 2 MB with redirect re-validation, and native workspace IO skills (`vraxter-read-file`, `vraxter-list-dir`, `vraxter-patch-code`) are confined via `os.OpenRoot`.
- **Hermetic Offline Toolchains**: Skill compilation uses randomized temporary build directories (`0700`), enforces `GOPROXY=off` and `GOSUMDB=off`, and blocks background SDK auto-downloads when running air-gapped.
- **Human-In-The-Loop (HITL) Approvals**: Enabling `require_skill_approval` (or running `elevated` host commands) halts execution over the gRPC stream and emits a `SKILL_APPROVAL_REQUEST`, rendered in the TUI's interactive Decision Bridge before execution proceeds.
- **Daemon Authentication & Scoped Tokens**: Local IPC endpoints use an auto-generated master key (`~/.vraxter/daemon.key`, `0600`), while external clients use granular scoped API tokens enforced across both unary and streaming RPCs.
- **Cryptographic Verification & Dry-Run Pipeline**: Every skill undergoes a zero-capability sandboxed dry-run before its SHA-256 checksum is persisted, and `vraxter skills inspect` / `Runner.Execute` verify on-disk SHA-256 integrity prior to instantiation.
- **Encryption at Rest & File Permissions**: Provider API keys (`providers.api_key`) are encrypted at rest in SQLite using AES-256-GCM; sensitive runtime artifacts (`daemon.key`, `vraxter.log`, `.wasm`, `.manifest.json`) use `0600` permissions. *(Full database encryption / SQLCipher and Ed25519 skill signatures are planned for the next hardening phase.)*

> [!NOTE]
> **Implementation Status & Experimental Stubs**: The core gRPC daemon (`:50051`), WASM runner, offline `vraxter skills inject`, air-gap/DDIL network shield, `HostExecGuard`, HITL gates, and TUI workstation are implemented and tested. The following components are currently experimental or placeholder stubs:
> - **HTTP `:8080` Connect RPC endpoint**: Currently exposes only the `EnvService` skeleton; full agent streaming and management run over gRPC (`:50051`).
> - **Remote Skill Hub (`vraxter skills install` / `download`)**: Placeholder stubs; use `vraxter skills inject` for offline/local `.wasm` and Manifest V1 packages.
> - **Google Home (`SpatialService`)**: Returns mock rooms/devices when enabled; local `spatial.json` mapping is active.
> - **World Feature Modules (`internal/features/*`)**: Build-tag scaffolding hooks; security policies are enforced directly by the core daemon and configuration flags.

## 📜 License & Commercial Use

Vraxter operates under a dual-licensing model:

1. **Open Source (AGPL v3):** Vraxter Core is free and open-source under the [GNU Affero General Public License v3.0](LICENSE). This ensures that the engine remains public, auditable, and accessible. Any modifications or integrations must also be open-sourced under the same license.
2. **Commercial License:** For enterprises, governments, or organizations that require closed-source modifications, proprietary integrations, or do not wish to adhere to the AGPL v3 requirements, a commercial license is available. 

> For commercial licensing (Vraxter Enterprise, Facility, Sovereign, etc.), please contact Vraxter.

### Contributing
To maintain this dual-licensing capability, all external contributions require agreeing to our [Contributor License Agreement (CLA)](CONTRIBUTING.md).

---

## ⚠️ Liability & Misuse Disclaimer

**Vraxter is a Sovereign Autonomous Engine with the capability to write, compile, and execute code dynamically on your local system or network.** 
If you grant Vraxter excessive permissions (such as root filesystem access or unfiltered network access via WASM capabilities) or attach it to physical infrastructure (like Facility Management Systems) without robust Skill Approval pipelines, **it poses a critical security risk.**

By using Vraxter, you acknowledge that Vraxter and its contributors are **not liable** for any damages, data loss, network compromise, or physical infrastructure failure caused by the autonomous actions of this software. You are strictly responsible for securing the execution sandboxes and monitoring the sub-agent swarms.

---

### Developed with Precision by [Vraxter](https://github.com/vraxter)
*Built for the transition to the Autonomous Era.*
