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

### 1. Client-Agnostic Connect RPC Interface
Vraxter operates as a completely headless backend daemon exposing a robust Connect RPC (HTTP/2) API. It is entirely uncoupled from any specific UI framework, allowing you to connect Thin CLI interfaces, Web Dashboards, IoT hardware, or SCADA systems natively.

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
| **Language** | Go (1.21+) | High-speed concurrency and single-binary portability. |
| **Sandbox** | Wazero (WASI V1) | Hardened, capabilities-based WebAssembly sandbox without local dependencies. |
| **Persistence** | SQLite | Local-first, relational storage for memory and configuration with WAL mode optimized for Go concurrency. |
| **TUI Interface** | Bubble Tea & Lip Gloss | Anodized Titanium & Burnished Copper terminal interface with double-rail command deck. |
| **Internal Comms** | Connect RPC & gRPC (HTTP/2) | Client-agnostic, high-speed API layer using Protocol Buffers. |

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
- `vraxter config list`: Inspect global settings, active isolation mode (`airgap`, `local_lan`, `hybrid`), and perimeter lock status.
- `vraxter config set isolation_mode <airgap|local_lan|hybrid>`: Configure network isolation boundary.
- `vraxter config set require_skill_approval true`: Enforce Human-In-The-Loop (HITL) manual skill approvals for strict execution governance.
- `vraxter providers <id> discover`: Discover available models for a specific provider.
- `vraxter providers setup`: Interactive wizard to set up a new provider.
- `vraxter providers add <name> --type <type>`: Add a provider non-interactively.
- `vraxter skills inject <path.wasm|skill_dir|manifest.json>`: Inject local skill binaries or Manifest V1 packages.
- `vraxter skills inspect <id>`: Verify cryptographic SHA-256 hash of a skill binary against the vault.
- `vraxter tokens create <name> --scopes="chat:write,skills:read"`: Generate scoped API tokens for external clients.

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

Vraxter implements a **Secure-by-Design** philosophy for Enterprise environments:
- **Deterministic Zero-Egress Air-Gap**: Operates on a pure socket- and resolver-level isolation model without external ping checks. In-memory IP inspection blocks public domains before emitting any DNS queries, preventing data exfiltration via DNS tunneling and TOCTOU DNS rebinding.
- **Internal Private LAN Supported**: The air-gap perimeter isolates Vraxter from the public WAN while permitting trusted communication within your private corporate intranet or home lab (RFC 1918: `192.168.x.x`, `10.x.x.x`, `172.16.x.x`, and `.local` mDNS). Operators can seamlessly connect to dedicated GPU clusters running Ollama, vLLM, or llama.cpp on the local network.
- **Anti-Downgrade & Perimeter Lockout**: When executed with `--airgap` or `VRAXTER_AIRGAP=true`, the isolation perimeter becomes immutable. Any runtime attempts to downgrade security to hybrid mode over the API are rejected, requiring an administrative daemon restart.
- **WASM Manifest V1 Capabilities Sandbox**: Tools run strictly inside Wazero using the official `skill-manifest/1` specification. Storage pools (`workspace`, `input`, `output`, `skill_data`, `ephemeral`) enforce virtualized read-only or read-write filesystem limits.
- **Hermetic Offline Toolchains**: Skill compilation enforces `GOPROXY=off` and `GOSUMDB=off`, while the Toolchain Manager prevents unauthorized background SDK downloads when running air-gapped.
- **Human-In-The-Loop (HITL) Approvals**: Enabling `require_skill_approval` halts execution over the Connect RPC stream and emits a `SKILL_APPROVAL_REQUEST`, rendered in the TUI's interactive Decision Bridge before any dynamic code or WASM payload executes.
- **Daemon Authentication & Scoped Tokens**: Local IPC endpoints use an auto-generated master key (`~/.vraxter/daemon.key`), while external dashboards and IoT clients use granular, dynamically generated API Tokens (e.g. `skills:read` scopes) to eliminate lateral privilege escalation.
- **Cryptographic Verification**: `vraxter skills inspect` validates SHA-256 hashes of WASM binaries against database checksums before execution.
- **Dry-Run Validation Pipeline**: Every skill undergoes a sandboxed dry-run execution verification before being signed and persisted to prevent corrupted or malicious binaries from entering the local filesystem.
- **Local Sovereignty**: All API keys are encrypted at rest using AES-256-GCM.

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
