# Vraxter Specialist Management

**Specialists** are high-fidelity sub-agents designed for deep domain expertise. They allow Vraxter to scale its "brain" by delegating complex engineering, security, and architectural workflows to dedicated contexts.


## The Specialist Lifecycle

### 1. Creation & Registration
Specialists can be registered through two methods:
- **Interactive TUI Wizard (`/specialists add`)**: Launches the `◈ SPECIALIST ARCHITECTURE WIZARD`, stepping the operator through archetype selection (`SRE / Systems Architect`, `Frontend / UI Specialist`, `Custom Domain Specialist`), identifier assignment, custom capability tags, and model binding.
- **Autonomous Tool (`vraxter-create-specialist`)**: When prompted in natural language, Vraxter generates the identifier, structured expertise scope, and specialized system prompt, persisting it to SQLite with AES-256 encryption.

### 2. Delegation Modes

#### Explicit Operator Delegation (`@mention`)
Operators can explicitly route any prompt to a specialist by prefixing with `@`:
```text
@security-auditor Review the cryptographic signature validation in internal/auth/auth.go
```
Typing `@` into the TUI input deck immediately pops open the **Specialist Swarm Roster Autocomplete Overlay**, displaying all active sub-agents and their domain scopes.

#### Autonomous Supervisor Delegation (`vraxter-delegate`)
When the Supervisor (Main Vraxter) identifies that a query requires specialized expertise:
1. It invokes the `vraxter-delegate` tool with the target specialist handle and task payload.
2. The TUI switches its focus capsule to `FOCUS: @<SPECIALIST_NAME>`.
3. The specialist takes over the conversation context, rendering responses in dedicated left-accent copper blocks.

#### Multi-Phase Planner Delegation (`/plan`)
During architectural planning, the Planner decomposes complex objectives into atomic phases and assigns each phase to either the `supervisor` or specific domain specialists (e.g., `specialist_id: "auditor"`).

### 3. Handoff Recovery & Dynamic Identity
When a specialist completes its scoped task or encounters out-of-domain requirements, it calls `vraxter-return-control`:
- **Boundary Resumption**: The TUI clears the specialist latch and renders:
  ```text
  ◈ Supervisor Resumed: Specialist boundary reached. Control returned to Vraxter
  ```
- **Context Isolation**: The Orchestrator automatically recalibrates conversation memory and restores the base Supervisor persona directives, preventing identity drift and context leakage.


## Specialist History Isolation

Vraxter maintains strict semantic boundaries between sub-agents:
- **Conversation Tracking**: Specialist interactions are tagged in the SQLite repository with their `specialist_id`, ensuring sub-agent memory fragments do not pollute global conversation context.
- **RAG Recalibration**: Vector and semantic memory searches recalibrate dynamically depending on whether the active focus is the Supervisor or a specialized persona.


## Built-in Archetypes & Use Cases

| Archetype | Focus Domain | Key Capabilities |
| :--- | :--- | :--- |
| **Security Auditor** | Vulnerability analysis & compliance | CWE-top-25, cryptographic reviews, air-gap perimeter validation |
| **SRE / Systems Architect** | High-availability infrastructure | Kubernetes, Docker, Linux networking, distributed resilience |
| **UI Specialist** | Terminal & graphical interfaces | Bubble Tea / Lip Gloss, ergonomics, ANSI color harmony |
| **Custom Specialist** | Operator-defined workflows | Custom prompt directives, dedicated model overrides |

---

## ◈ Command Reference

- `/specialists` or `/specialists list`: View all registered specialists and their models.
- `/specialists add` (or `create`, `new`): Launch the interactive Specialist Architecture Wizard.
- `/specialists delete <id>` (or `remove <id>`): Retire a specialist and remove from storage.
- `@<specialist-id> <message>`: Directly address a specialist from the command deck.
