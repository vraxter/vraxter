# Vraxter TUI Workstation

The Vraxter Terminal User Interface (TUI) is a high-fidelity, sovereign engineering workstation built on Charm's `Bubble Tea` and `Lip Gloss` frameworks. It serves as the primary cockpit for human operators interacting with the headless Vraxter engine.

---

## 1. Visual Design System: Anodized Titanium & Burnished Copper

Vraxter avoids generic terminal styling in favor of a bespoke, industrial hardware aesthetic:
- **Chassis**: Anodized Titanium (`#13161B`, `#1C2028`, `#2A303C`) with deep Obsidian backing (`#07080A`, `#0B0D10`).
- **Specular Highlights**: Specular Copper (`#F7D4B2`) and Burnished Amber (`#E2A576`, `#C87D4B`).
- **Telemetry Indicators**: High-contrast LEDs in Emerald (`#38FFA3`) for active air-gap defense, Sapphire (`#7AA2F7`) for local networks, and Crimson (`#FF3864`) for security halts.
- **Zero-Bleed Surface Rendering**: Strict ANSI parameter bounding ensures that background fills, borders, and margins never bleed across terminal row boundaries or cause ANSI escape sequence panics.

---

## 2. The Double-Rail Command Deck

Located at the bottom of the workstation, the **Double-Rail Command Deck** anchors the operator's workspace:

```
── /? HELP • Shift+↵ NEWLINE • ^C QUIT ─── [ 🛡️ AIRGAP LOCKED ] FOCUS: SUPERVISOR FOCUS ──
❯ Type a command or question for Vraxter...
```

### Top Rail Components (`.deck-top-rail`)
1. **Shortcut Bar**: Displays context-sensitive controls (`Shift+↵` for multiline input, `^C` to quit, `←/→` in decision mode).
2. **Background Jobs Pill**: When long-running daemons or sub-agents run in the background, a live indicator displays: `⚙ 2 RUNNING: textstats, netscan`.
3. **Ambient Isolation Capsule**: Communicates network boundary status in real-time:
   - `[ 🛡️ AIRGAP LOCKED ]`: Emerald LED badge. Zero egress to public WAN, private LAN allowed.
   - `[ 🌐 LAN ONLY ]`: Sapphire badge. Local private subnets active.
   - `[ ⚡ HYBRID ]`: Copper badge. Cloud providers permitted with HITL approval gates.
4. **Focus Identity Pill**: Indicates which intelligence currently holds session focus:
   - `FOCUS: SUPERVISOR FOCUS`: Central reasoning orchestrator.
   - `FOCUS: @frontend-pro`: Domain specialist sub-agent.
   - `FOCUS: VRAXTER SHIELD (HITL GATE)`: Security gate awaiting operator confirmation.

### Input Shelf (`.tui-input-shelf`)
- **Dynamic Textarea**: Automatically expands from 1 to 5 lines as the operator types.
- **Interactive Slash Overlay (`/`)**: Typing `/` opens an instant filterable menu of workstation commands.
- **Specialist Autocomplete Shelf (`@`)**: Typing `@` triggers real-time autocomplete for available sub-agents.

---

## 3. Welcome Header & Ambient Telemetry (`.tui-welcome-header`)

Rendered at the top of the terminal viewport:

```
 Welcome back, Operator!
 Session: a7f8c290-b611-40ea-9e32-2d93e1b01789

 ━ System Ready | 🛡️ Air-Gap Active (Zero Egress Perimeter)
```

The status block dynamically updates during the reasoning lifecycle:
- `━ System Ready | 🛡️ Air-Gap Active`: Engine is ready for queries.
- `━ Vraxter is Planning Strategy...`: Multi-phase planner decomposing a complex goal.
- `━ Vraxter is Thinking...`: Active streaming from local or configured models.

---

## 4. The Decision Bridge & HITL Approval Shelf (`.tui-input-shelf.is-approval`)

When `require_skill_approval` is active or a multi-phase architectural plan is proposed, the input shelf transforms into the **Interactive Decision Bridge**:

```
🔒 ENTERPRISE POLICY: MANUAL SKILL APPROVAL REQUIRED              ● WAITING FOR INPUT DECISION
──────────────────────────────────────────────────────────────────────────────────────────────
SKILL ID & BINARY: lab.textstats (Wazero WASI Sandbox)   PERIMETER: 🛡️ AIRGAP (ZERO EGRESS)

◆ INVOCATION PAYLOAD
{
  "files": ["/input/report.txt"]
}
──────────────────────────────────────────────────────────────────────────────────────────────
[ ✓ ACCEPT / PROCEED (↵) ]    [ ✕ DENY / REJECT (ESC) ]    [ AMEND ]
```

Operators navigate options using `←` / `→` arrow keys or confirm instantly with `Enter` / `Esc`.

---

## 5. Execution Banners (`.tui-execution-confirmed`)

System telemetry, background jobs, and execution confirmations are rendered as brushed telemetry blocks:

```
✓ ARCHITECTURE PLANNER (@SECURITY-AUDITOR)  All phases executed with zero perimeter egress.  AIRGAP VERIFIED
```

---

## 6. Complete Slash Command Reference

| Command | Arguments | Description |
| :--- | :--- | :--- |
| **`/start`** | — | Launches the unified onboarding flow (Profile ➔ Providers ➔ Models). |
| **`/help`, `/?`** | — | Displays the interactive keyboard and command reference. |
| **`/airgap`, `/isolation`** | — | Displays live telemetry on socket dialer, DNS filters, and zero-egress state. |
| **`/plan`** | `<task>` | Decomposes complex goals into multi-phase `.vrx` implementation plans. |
| **`/specialists`** | `[list/add/delete]` | Manages domain sub-agents (or delegate directly via `@<name>`). |
| **`/skills`** | `[list/add/inject]` | Manages WASM skill catalog (`/skills add <name> <spec>` for synthesis). |
| **`/providers`** | `[list/setup/add]` | Configures LLM providers (Google, OpenAI, Ollama, Custom). |
| **`/models`** | `[list/setup/add]` | Registers and sets priority order for reasoning models. |
| **`/activate`** | `<id_or_alias>` | Manually overrides and pins the active model for the session. |
| **`/restore`, `/auto`** | — | Restores automatic use-case driven model routing. |
| **`/jobs`** | — | Lists background daemon tasks and execution statuses. |
| **`/history`** | — | Displays past conversation sessions stored in the SQLite vault. |
| **`/load`** | `<#>` | Replays a conversation transcript using its row index from `/history`. |
| **`/session`** | `<session_id>` | Switches active context to a different session ID. |
| **`/clear`** | — | Clears the chat viewport while preserving memory in the database. |
| **`/factory-reset`** | `[--confirm]` | Wipes local database and keys, restoring clean defaults with an automatic backup. |
| **`/quit`, `/exit`** | — | Gracefully disconnects and closes the workstation. |

---

## 7. Real-Time Architecture

The TUI connects to the headless `vraxter` daemon over **Connect RPC / gRPC (HTTP/2)**:
- **Zero-Latency Streaming**: LLM tokens render in real time without buffering.
- **Asynchronous Global Event Bus**: Background task completions, tool invocations, and supervisor handoffs arrive as push notifications, eliminating CPU-intensive polling loops.
