# VRAXTER

### **The Autonomous Engineering Workstation**
*Engineering Autonomy. Standardizing Intelligence.*

Vraxter is a **Local-First, Autonomous Agent Workstation** designed to bridge the gap between high-level LLM reasoning and native system execution. It is a persistent engine that interprets intent, orchestrates specialized sub-agents, and automates technical workflows within a hardened, privacy-centric environment.


## Core Architecture

Vraxter is built on the **Sovereign-Agent Model**, ensuring that your data, logic, and execution stay within your infrastructure.

### 1. The Autonomous Execution Loop
Vraxter doesn't just generate text; it solves problems by iterating through an autonomous cycle:
- **Analyze & Plan**: Decomposes complex queries into actionable phases using its internal **Planner**.
- **Resolve Intent**: Routes tasks either to local scripts or specialized LLM instructions.
- **WASM Skill Execution**: Runs highly performant, sandboxed tools compiled to WebAssembly (WASM).
- **Self-Recursion (The Coder)**: If a tool is missing, Vraxter can write, compile, and register a new Skill in real-time.

### 2. Specialist Swarm
Vraxter manages a roster of **Specialists**, domain-specific sub-agents (e.g., Security Auditor, Frontend Architect) that can be delegated to for hyper-focused reasoning. The Supervisor engine orchestrates handoffs and consolidates results seamlessly.

### 3. Multi-Model Intelligence
Stop being locked into a single provider. Vraxter features dynamic, priority-based routing across:
- **Cloud**: OpenAI, Anthropic, Google.
- **Local**: Ollama.
- **Failover**: Automatic fallback to secondary models if a provider is unreachable.


## 🚀 Getting Started

### Installation
Vraxter is distributed as a single high-performance binary.

```bash
# Clone the repository
git clone https://github.com/PatagonicRune/vraxter.git
cd vraxter

# Build the workstation
go build -o bin/vraxter ./cmd/vraxter/main.go
```

### The Onboarding Wizard
The first time you run Vraxter, or by typing `/start`, you will enter the **Premium Onboarding Flow**. This TUI-native wizard handles:
1. **User Profile**: Defining your expertise and preferred interaction style.
2. **Provider Setup**: Securely managing API keys and base URLs.
3. **Model Registration**: Configuring your reasoning engines and use-case priorities.

```bash
./bin/vraxter
# Once inside the TUI, type:
/start
```

## Technical Stack

| Component | Technology | Rationale |
| :--- | :--- | :--- |
| **Language** | Go (1.21+) | High-speed concurrency and single-binary portability. |
| **Sandbox** | Wazero (WASM) | Hardened, sandboxed tool execution without local dependencies. |
| **Persistence** | SQLite | Local-first, relational storage for memory and configuration. |
| **TUI Interface** | Bubble Tea | Premium, fluid terminal ecosystem for professional engineers. |
| **Internal Comms** | gRPC / Protobuf | High-speed, type-safe daemon/client communication. |

## Command Reference

Vraxter features a sophisticated "Slash Command" system for workstation management:

| Command | Description |
| :--- | :--- |
| `/start` | Launches the unified onboarding/setup wizard. |
| `/help` | Displays the interactive command documentation. |
| `/plan <query>` | Forces Vraxter to generate a structured implementation strategy. |
| `/specialists` | Manages the roster of domain-specific sub-agents. |
| `/providers` | Adds or modifies LLM provider configurations. |
| `/models` | Registers and prioritizes specific reasoning models. |
| `/clear` | Resets the current session context while maintaining memory. |

## Full Documentation

For deep dives into Vraxter's internals, development guides, and security model, explore our complete documentation suite:

- [**Architecture Guide**](docs/ARCHITECTURE.md): Distributed systems and gRPC data flow.
- [**VRX Protocol Spec**](docs/VRX_PROTOCOL.md): Mastery of neural prompt templating.
- [**Skills Development**](docs/SKILLS_GUIDE.md): Building and registering WASM-based tools.
- [**Specialist Swarm**](docs/SPECIALISTS.md): Managing autonomous sub-agents and delegation.
- [**Vraxter Shield**](docs/SECURITY.md): Security, isolation, and data sovereignty.



## 🛡️ Vraxter Shield (Security Architecture)

Vraxter implements a **Secure-by-Design** philosophy:
- **Sandbox Isolation**: The WASM runtime restricts tool access to specific file paths using strict capabilities-based security.
- **Checksum Verification**: Every Skill binary is hashed (SHA-256) to prevent local tampering or hijacking.
- **Local Sovereignty**: All API keys are encrypted at rest using AES-256-GCM.

---

### Developed with Precision by [Patagonic Rune](https://github.com/PatagonicRune)
*Built for the transition to the Autonomous Era.*