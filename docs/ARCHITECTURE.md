# Vraxter Architecture Guide

Vraxter is engineered as a **distributed, headless autonomous engine** consisting of a persistent background daemon and a high-performance terminal interface.


## System Overview

The system follows a headless Client-Server architecture over **gRPC (HTTP/2)** on `127.0.0.1:50051` (with an experimental Connect RPC HTTP endpoint on `127.0.0.1:8080` currently serving the `EnvService` skeleton), ensuring that the "brain" (state, memory, execution) remains decoupled from any specific frontend (TUI, web dashboard, or external client).

### 1. The Daemon (`vraxter server`)
The daemon is the primary source of truth. It manages:
- **Persistence**: SQLite-backed history, specialists, and model configurations in WAL mode (`synchronous=FULL`, `foreign_keys=ON`, `VACUUM INTO` backups) with AES-256-GCM encryption for `providers.api_key`.
- **Environmental Context Engine**: A thread-safe `StateManager` that manages dynamic `ModeConfig` templates (e.g. ambient lighting, music).
- **Orchestration**: The request lifecycle from intent resolution to multi-phase execution and graceful shutdown (`SYSTEM_SHUTDOWN` / OS signals).
- **Skill Runner & HostExecGuard**: The Wazero-powered WASM sandbox supporting Go, Rust, and Zig, strictly bounded by Skill Manifest V1 capability declarations, `Tier3Unverified` fail-closed defaults, scoped storage pools (`workspace`, `input`, `output`, `skill_data`, `ephemeral`), `os.OpenRoot` confinement, and optional allowlisted host command execution (`--enable-host-exec`).
- **Intelligence**: Model routing and multi-provider coordination with DDIL HTTP timeouts.
- **Perimeter & Isolation (Vraxter Shield)**: Real-time network boundary enforcement (`airgap`, `local_lan`, `hybrid`), in-memory DNS inspection, IMDS link-local blocking, CIDR whitelist support, transport-level socket blocking (`NewAirgapDialer`), and anti-downgrade runtime locking.
- **Auth Pipeline**: Unary and streaming scoped-token enforcement (`wrappedServerStream`) and master key dual-authentication (`daemon.key`).

### 2. The gRPC & Connect RPC Layer
- **Protobuf-Defined**: Every interaction is type-safe and defined in `api/v1`.
- **Primary gRPC (HTTP/2 on `:50051`)**: Serves `AgentService`, `ProviderService`, `ModelService`, `SkillService`, `ConfigService`, and `AuthService` for the TUI, CLI, and external thin clients.
- **Experimental Connect RPC (`:8080`)**: Currently serves the `EnvService` HTTP skeleton with `HTTPAuthMiddleware` and hardened HTTP timeouts.
- **Streaming**: Supports server streaming for real-time LLM token delivery, tool progress, and interactive human-in-the-loop approvals.

### 3. The Global Event Bus
Vraxter implements a centralized, asynchronous event streaming pipeline. The Event Bus emits standardized events (`SKILL_APPROVAL_REQUEST`, `TOOL_CALL`, `SPECIALIST_RESULT`, system statuses) ensuring all connected clients react in real-time without polling.

### 4. Spatial & Identity Concurrency
The engine natively supports serving multiple physical rooms simultaneously.
- **Spatial Isolation**: Requests containing a `source_zone` (e.g. "living_room") instantiate separate conversation memory streams.
- **Unified Device Mapping**: The `SpatialService` loads internal `spatial.json` room-to-device mappings. *(Google Home API integration is currently a mock stub when `enable_google_home` is toggled.)*
- **Voice Recognition**: Requests can pass a `source_user`. The Orchestrator automatically fetches that exact User Profile from the database, instantly hot-swapping the AI's identity context even during concurrent multi-user conversations.

### 5. Terminal Workstation (TUI)
Built with Bubble Tea and Lip Gloss following the **Anodized Titanium & Burnished Copper** design system:
- **Double-Rail Command Deck**: Top rail monitors session ID, active provider/model, and live isolation status capsule (`[ 🛡️ AIRGAP LOCKED ]`, `[ 🌐 LAN ONLY ]`, `[ ⚡ HYBRID ]`); bottom rail hosts the primary interaction shelf.
- **Decision Bridge**: Modal approval shelf for human-in-the-loop authorization (`[Y] Execute (WASM Sandbox)` / `[N] Deny Request`).
- **Specialist Swarm UI**: Real-time `@<name>` autocomplete overlays, left-accent specialist message blocks, and dynamic supervisor handoffs.


## The Intelligence Pipeline

Every user request goes through the following stages:

### A. Intent Resolution
Vraxter first determines if the input is:
- **Explicit Command**: Starts with `/` (e.g., `/airgap`, `/plan`, `/specialists`, `/models`).
- **Direct Specialist Delegation**: Starts with `@` (e.g., `@security-auditor audit this`).
- **Direct Skill Call**: Starts with `!` (e.g., `!textstats`).
- **Neural Reasoning**: Natural language query requiring LLM planning or execution.

### B. Planning (Pro-Mode)
For complex queries, the **Planner** breaks the task into a series of **Phases**. Each phase is an atomic goal assigned to either the supervisor or a specific **Specialist**.

### C. Context Assembly
Vraxter dynamically builds a system prompt using:
- **User Profile**: Expertise and interests.
- **Semantic Memory**: RAG-based context from conversation history.
- **Project Structure**: Automated Git context and file tree analysis.
- **Capabilities Matrix**: Listing available WASM tools and active models.

### D. Perimeter Shielding & Execution
Before outbound network calls are made:
- If `isolation_mode: "airgap"` is active, public WAN IP connections are blocked with `syscall.EPERM` by `NewAirgapDialer`. Private LAN endpoints (RFC 1918: `192.168.x.x`, `10.x.x.x`, `172.16.x.x`, `.local`, `.lan`) remain accessible for local Ollama/vLLM servers.


## Data Flow Diagram

```mermaid
graph TD
    User([User Input]) --> Client[TUI / Web UI / Thin Client]
    Client --> ConnectRPC["gRPC :50051 / Connect Env :8080"]
    ConnectRPC --> Daemon["vraxter server"]
    Daemon --> Orchestrator{Orchestrator}
    Orchestrator --> StateMgr[State Manager]
    Orchestrator --> SpatialSvc[Spatial Service]
    Orchestrator --> SQL["SQLite WAL + AES-256 Keys"]
    Orchestrator --> Runner[WASM Skill Runner]
    Orchestrator --> Router[LLM Multi-Router]
    
    Router --> Shield{Vraxter Shield}
    Shield -- "Hybrid / Permitted" --> Cloud[Public Cloud APIs]
    Shield -- "Airgap / Private LAN" --> Local[Local / Intranet Ollama / vLLM]
    Shield -- "Blocked WAN (Airgap)" --> Block[EPERM Drop]
    
    Runner --> Sandbox((WASM Manifest V1 Sandbox))
```
