# Vraxter Skills Development Guide

A **Skill** in Vraxter is an executable plugin that allows the agent to perform real-world actions. Vraxter supports both **WASM Isolation** and **Native OS** execution engines.


## WASM Isolation Engine (Preferred)

Vraxter uses the **Wazero** runtime to execute skills in a hardened sandbox. This is the safest way to extend Vraxter's capabilities.

### 1. Requirements
- A language that compiles to WASM/WASI (Go, Rust).
- No direct host access (Network/FS access must be granted via Permissions).

### 2. Implementation Pattern (Go Example)
A skill must listen to **Stdin** for a JSON-RPC 2.0 request and respond on **Stdout**.

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	// 1. Read the JSON-RPC request from Vraxter
	// 2. Perform logic
	// 3. Write JSON-RPC response to Stdout
	fmt.Print(`{"jsonrpc": "2.0", "result": {"output": "Success!", "status": "completed"}, "id": "my-skill"}`)
}
```

### 3. Permission System
Permissions are declared in the Skill Manifest:
- `fs_read:/root/path`: Grants read-only access to a directory.
- `fs_write:/root/path`: Grants read-write access.
- `network`: (Future) Grants outbound HTTP access.


## The Autonomy Loop: `vraxter-coder`

Vraxter's most powerful feature is its ability to **generate its own skills**.
When you ask Vraxter to do something it doesn't have a tool for:
1. It analyzes the requirement.
2. It generates the Go or Rust code for a new tool.
3. It compiles the code to WASM.
4. **Dry-Run Validation Pipeline**: The engine immediately executes the compiled WASM binary in a sandboxed test environment. If the skill crashes or fails validation, it is automatically purged.
5. Once verified, it registers the tool in its local DB and records its SHA-256 checksum for secure execution.

## Multimodal Official Skills

Vraxter ships with native sensory capabilities built directly into the Orchestrator as **Official Skills**. These do not require WASM compilation and hook directly into Vraxter's spatial awareness matrix:

- **`vraxter-see`**: Analyzes and describes visual content from image data URIs.
- **`vraxter-hear`**: Transcribes audio inputs (e.g., from a microphone in a specific room) into text for processing.
- **`vraxter-talk`**: Synthesizes speech from text and intelligently routes the audio playback to the specific `source_zone` (or custom speaker) where the request originated.


## Registering a Skill Manually

Skills can be added via the CLI:
```bash
vraxter add --id my-tool --desc "Scans for logic errors" --cmd "./bin/tool.wasm" --engine wasm
```

### Manifest Fields:
- **`ID`**: Unique identifier (e.g., `git-diff-analyzer`).
- **`Engine`**: `wasm` or `native`.
- **`Checksum`**: SHA-256 hash (Required for unverified skills).
- **`Tier`**: Defines the trust level (Official vs. Unverified).
