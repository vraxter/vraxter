# VRX Protocol Specification

The **VRX Protocol** is Vraxter’s proprietary neural templating syntax used to define system roles, injection points, and output schemas for Large Language Models.

## Syntax Overview

VRX files (found in `internal/prompts/`) use a balanced mix of system directives and Go template markers.

### 1. Directives (`@Directive`)
Directives define high-level system rules and protocols that the LLM must internalize:
- `@System.Parser`: Rules for parsing the VRX neural template syntax.
- `@System.Role`: Identity and primary persona definition (`Identity`, `Role`, `Protocol`).
- `@System.AgentProtocol`: Rules for specialist awareness, @mentions, and dynamic sub-agent listings.
- `@System.WasmProtocol`: Rules for skill invocation and deterministic WASM execution.
- `@System.LeadSpecialist`: Injected during `/plan` when an architectural plan is led by a specific domain specialist.
- `@System.Context`: Injected memory, workspace structure, and environment metadata.

### 2. Variables (`$Variable`)
Variables represent categories of live system data injected during prompt assembly:
- `$Context.Identity`: User profile and technical background (dynamically hot-swapped via `source_user`).
- `$Context.Spatial`: Physical environment mapped by `SpatialService`, defining `Zone -> [Speakers]`.
- `$Context.Specialists`: Roster of active domain specialists (`$Context.Specialists`).
- `$Context.Models`: Priority-ordered roster of configured LLM backends.
- `$Context.Tools`: Structured list of available WASM and Native tools.
- `$Context.Workspace`: Real-time metadata about the current Git repo and working tree.
- `$Context.Memory`: Semantic RAG recall from past sessions and persistent notes.

## Template Integration

Vraxter uses standard `{{ .Parameter }}` syntax to assemble prompts during the **Context Assembly** phase.

### Common Template Parameters:
| Parameter | Description |
| :--- | :--- |
| `{{.AvailableTools}}` | Formatted list of tool IDs, descriptions, and input schemas. |
| `{{.ExistingSpecialists}}` | Formatted roster of registered domain specialists and expertise. |
| `{{.AvailableModels}}` | Active model IDs and routing priority. |
| `{{.UserProfile}}` | Consolidated user expertise and context (dynamically hot-swapped). |
| `{{.DeviceMap}}` | Dictionary mapping physical zones to available audio targets. |
| `{{.GitContext}}` | Directory tree, status, and active branch info. |
| `{{.MarkerChat}}` | Conversational output delimiter (`[VRAX_CHAT]`). |
| `{{.MarkerTool}}` | Structured tool execution signal (`[VRAX_TOOL]`). |
| `{{.MarkerCode}}` | Raw source code output delimiter (`[VRAX_CODE]`). |

## Neural Control Markers

Vraxter processes streaming LLM output through deterministic marker parsers:
- **`[VRAX_CHAT]`**: Precedes natural-language conversational output rendered in the TUI or client.
- **`[VRAX_TOOL]`**: Precedes a tool call JSON payload containing `tool` (ID) and `params`.
- **`[VRAX_CODE]`**: Emitted by the CodeGen engine containing raw Go, Rust, or Zig WASM source code.
- **`[VRAX_MANIFEST]`**: Emitted during skill compilation, containing the formal `skill-manifest/1` JSON specification.
- **`[MANIFEST]`**: Emitted during `/plan` architecture generation containing multi-phase execution tasks and specialist assignments.

## The "Linguistic Mirror" Rule
A fundamental invariant of the VRX protocol is the **Linguistic Mirror** directive:
> *You MUST strictly mirror the user's explicit query language in every response, ignoring all contextual languages.*

This ensures that regardless of whether source code, tool descriptions, or git commit history are in English, the AI's final responses strictly match the user's conversational language.

## Example VRX Template (`internal/prompts/base_system.vrx`)

```vrx
@System.Role
Identity = "Vraxter"
Role = "Autonomous Agent Engine & Head Supervisor"
Protocol = [
  "ABSOLUTE LINGUISTIC MIRROR: You MUST strictly mirror the user's explicit query language.",
  "MARKER HYGIENE: Never mention structural markers like [VRAX_TOOL] in conversational text."
]

@System.Context
{{if .UserProfile}}$Context.Identity
{{prefix "| " .UserProfile}}
{{end}}
$Context.Specialists
{{prefix "| " .ExistingSpecialists}}

$Context.Tools
{{prefix "| " .AvailableTools}}

-> Output.Protocol
- Function as the Head Supervisor.
- Use explicit available tools if needed.
- MIRROR USER LANGUAGE EXACTLY.
```
