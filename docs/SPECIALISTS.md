# Vraxter Specialist Management

**Specialists** are high-fidelity sub-agents designed for specific domain expertise. They allow Vraxter to scale its "brain" by delegating complex tasks to specialized contexts.


## The Specialist Lifecycle

### 1. Creation
Specialists can be created via `/specialists join` or via the `vraxter-create-specialist` tool.
- **ID**: A short unique handle (e.g. `frontend-pro`).
- **Expertise**: A single paragraph defining what the sub-agent "knows".
- **Prompt**: The dedicated system instructions for this sub-agent.

### 2. Delegation (Handoffs)
When the Supervisor (Main Vraxter) identifies that a query falls within a specialist's domain:
1. It invokes the `vraxter-delegate` tool.
2. The UI focus shifts to the specialist banner.
3. The specialist takes over the conversation flow.

### 3. Handoff Recovery
Specialists are instructed to return control to the Supervisor when:
- The task is completed.
- The query goes out of their domain.
- The user explicitly asks for "Vraxter".

## History Isolation

Vraxter maintains **Semantic Boundaries** between specialists:
- **Conversation Tracking**: Specialist interactions are stored in the database with their `specialist_id` to ensure that context doesn't "leak" across domains.
- **Memory Recalibration**: When switching back to the Supervisor, the memory search is recalibrated to focus on general system logic rather than domain-specific fragments.

## Professional Use Cases

| Specialist | Expertise | Instructions |
| :--- | :--- | :--- |
| **Architect** | High-level system design | Focus on SOLID principles and design patterns. |
| **Auditor** | Security and best practices | Look for CWE-top-25 vulnerabilities and style violations. |
| **DevOps** | Infrastructure and CI/CD | Expert in YAML, Docker, and Kubernetes networking. |

---

## ◈ Commands
- `/specialists list`: View all registered sub-agents.
- `/specialists add`: Interactively create a new pro-agent.
- `/specialists delete <id>`: Retire a specialist.
