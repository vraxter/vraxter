# Vraxter TUI Workstation

The Vraxter Terminal User Interface (TUI) is a high-fidelity, professional engineering workstation. Built with Go's `Bubble Tea` framework, it serves as the primary gateway for users to interact with the headless Vraxter engine.

## Core Features

### 1. The Input Shelf & Overlays
Vraxter replaces standard CLI prompts with a dynamic, context-aware input shelf. It includes two primary overlays to streamline interaction:
- **Slash Commands (`/`)**: Typing `/` instantly opens an interactive command overlay. This allows users to execute workstation management commands (e.g., `/start`, `/specialists`, `/plan`) without needing to memorize syntax.
- **Specialist Autocomplete (`@`)**: Typing `@` triggers a real-time autocomplete overlay listing available sub-agents. This allows users to quickly force a delegation handoff to a specific domain expert (e.g., `@frontend-pro`).

### 2. Semantic Handoffs & Visual Integrity
When the main Vraxter Supervisor delegates a task to a specialist, raw JSON technical signals (like tool calls) are never exposed to the user.
- **Status Masking**: The TUI intercepts internal signals and replaces them with semantic, user-friendly status messages (e.g., "Delegating task to Security Auditor...").
- **Banner Alignment**: When a specialist takes over, the UI dynamically shifts focus to a specialized banner, maintaining a professional workspace aesthetic.

### 3. The "Crimson Sigil" Identity
To provide a distinct visual identity, Vraxter employs a post-processing injection step during rendering. The "Crimson Sigil" mascot icon is dynamically rendered as a high-fidelity, colored signature in the chat history, replacing placeholder tokens to ensure a polished visual experience.

## Real-Time Architecture

The TUI connects to the `vraxterd` backend daemon via the **Global Event Bus** over **Connect RPC (HTTP/2)**.
This enables:
- **Streaming Responses**: Real-time token-by-token rendering of LLM outputs.
- **Asynchronous Job Notifications**: The UI instantly updates when background processes (like skill execution or multi-agent handoffs) emit status events, without requiring manual polling.
