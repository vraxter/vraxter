# VRX Protocol Specification

The **VRX Protocol** is Vraxter’s proprietary neural templating syntax used to define system roles, injection points, and output schemas for Large Language Models.

## Syntax Overview

VRX files (found in `internal/prompts/`) use a balanced mix of system directives and Go template markers.

### 1. Directives (`@Directive`)
Directives define high-level system rules that the LLM must internalize.
- `@System.Parser`: Instructions on how the LLM should understand the VRX syntax.
- `@System.Role`: Identity and primary behavior definition.
- `@System.Context`: Rules for handling injected memory and workspace data.

### 2. Variables (`$Variable`)
Variables represent categories of data that Vraxter injects into the prompt.
- `$Context.Identity`: The user's dynamic profile. Overridden per-request if `source_user` voice recognition is active.
- `$Context.Spatial`: Physical layout mapped by the `SpatialService`, defining valid `Zone -> [Speakers]` targets.
- `$Context.Memory`: Results from semantic RAG searches.
- `$Context.Tools`: The structured list of available WASM and Native tools.
- `$Context.Workspace`: Real-time metadata about the current Git repo or project path.

## Template Integration

Vraxter uses standard `{{ .Parameter }}` syntax to inject live data during the **Context Assembly** phase.

### Common Parameters:
| Parameter | Description |
| :--- | :--- |
| `{{.AvailableTools}}` | Formatted list of tool IDs and descriptions. |
| `{{.UserProfile}}` | Consolidated user expertise and context (dynamically hot-swapped). |
| `{{.DeviceMap}}` | The formatted dictionary mapping physical zones to available speaker names. |
| `{{.GitContext}}` | Directory structure and active branch info. |
| `{{.MarkerChat}}` | The required marker for conversational text output. |
| `{{.MarkerTool}}` | The marker used for tool calls (e.g. `[VRAX_TOOL]`). |

## The "Linguistic Mirror" Rule
A fundamental part of the VRX protocol is the **Linguistic Mirror** directive:
> *You MUST strictly mirror the user's explicit query language in every response.*

This ensures that regardless of the language of the technical context (which may be in English), the final output matches the user's preferred language.

## Example VRX Block

```vrx
@System.Role
Identity = "Vraxter"
Role = "Head Supervisor"

@System.Context
{{if .UserProfile}}$Context.Identity
{{prefix "| " .UserProfile}}
{{end}}

-> Output.Protocol
- MIRROR USER LANGUAGE EXACTLY.
- Use explicit markers for tool calls.
```
