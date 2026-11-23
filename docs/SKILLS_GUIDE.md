# Vraxter Skills Development Guide

A **Skill** in Vraxter is an executable plugin that allows the agent to interact with filesystems, perform deterministic computations, and manipulate external environments. Vraxter runs all skills exclusively within a hardened **Wazero WebAssembly (WASI) Sandbox**.

---

## 1. Skill Manifest V1 Specification (`skill-manifest/1`)

Every modern skill in Vraxter is governed by a declarative, type-safe manifest conforming to the official schema (`schemas/manifest.schema.json`).

### Standard Manifest Example

```json
{
  "$schema": "https://vraxter.io/schemas/skill-manifest.v1.json",
  "manifest_version": "skill-manifest/1",
  "id": "lab.textstats",
  "name": "Text Stats",
  "version": "0.3.1",
  "description": "Counts words and lines of attached text files and writes a structured report.",
  "agent": {
    "risk": "low",
    "approval": "never"
  },
  "module": {
    "file": "textstats.wasm",
    "sha256": "84a3418c67bee2d03b306b973ef4d73cf0c5c4e365cb9dc8727d2c3df478fb12",
    "abi": "wasip1"
  },
  "routing": {
    "keywords": ["count", "words", "lines", "stats", "analyze"],
    "tags": ["text", "reporting"]
  },
  "io": {
    "input": {
      "via": "stdin",
      "format": "json",
      "schema": {
        "type": "object",
        "additionalProperties": false,
        "required": ["files"],
        "properties": {
          "files": {
            "type": "array",
            "maxItems": 100,
            "items": { "type": "string", "maxLength": 255 }
          }
        }
      }
    },
    "output": {
      "via": "file",
      "format": "json",
      "path": "/output/report.json"
    }
  },
  "capabilities": {
    "fs": {
      "mounts": [
        { "guest": "/input",  "source": "input",      "mode": "ro", "allow_extensions": [".txt", ".md"], "max_file_mb": 10 },
        { "guest": "/output", "source": "output",     "mode": "rw", "subpath": "reports", "max_total_mb": 5, "max_files": 20 },
        { "guest": "/cache",  "source": "skill_data", "mode": "rw", "max_total_mb": 20, "allow_delete": true },
        { "guest": "/tmp",    "source": "ephemeral",  "mode": "rw", "max_total_mb": 16 }
      ]
    },
    "env": {
      "vars": { "LANG": "C.UTF-8" }
    },
    "clock": {
      "wall": false,
      "monotonic": true
    },
    "random": false
  },
  "limits": {
    "memory_mb": 32,
    "max_runtime_ms": 10000
  }
}
```

### Manifest Sections Breakdown

| Section | Purpose |
| :--- | :--- |
| **`manifest_version`** | Must be `"skill-manifest/1"`. |
| **`agent`** | Controls autonomous governance: `risk` (`"low"`, `"medium"`, `"high"`) and `approval` (`"never"`, `"always"`, `"elevated"`). |
| **`module`** | Points to the compiled `.wasm` file, its ABI (`"wasip1"` or `"wasi_snapshot_preview1"`), and expected SHA-256 hash. |
| **`routing`** | Keywords and tags used by the Orchestrator's Intent Resolver to autonomously select this tool. |
| **`io.input`** | Defines invocation payload delivery (`via`: `"stdin"`) and validates arguments using Draft-07 JSON Schema. |
| **`io.output`** | Defines how the runner retrieves execution results (`via`: `"stdout"` or `"file"` with explicit virtual path). |
| **`capabilities.fs`** | Maps guest paths to host storage pools (`"workspace"`, `"input"`, `"output"`, `"skill_data"`, `"ephemeral"`). |
| **`capabilities.clock`**| Grants access to wall-clock or monotonic clocks. |
| **`capabilities.random`**| Determines whether cryptographic entropy (`crypto/rand`) is made available to the guest. |
| **`limits`** | Strict memory ceilings (pages/MB) and CPU timeout bounds (milliseconds). |

---

## 2. Implementation Pattern (Go Example)

A skill compiled with Go / TinyGo reads invocation parameters from `os.Stdin` and writes results to `os.Stdout` (or to an allocated `/output` mount):

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type InputPayload struct {
	Files []string `json:"files"`
}

type OutputResult struct {
	TotalFiles int      `json:"total_files"`
	ReportPath string   `json:"report_path"`
	Status     string   `json:"status"`
}

func main() {
	// 1. Read input payload from Stdin
	inputBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	var input InputPayload
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid input JSON: %v\n", err)
		os.Exit(1)
	}

	// 2. Perform isolated execution
	total := len(input.Files)

	// 3. Write structured output report to /output/report.json
	reportJSON := fmt.Sprintf(`{"analyzed_files": %d, "summary": "Complete"}`, total)
	_ = os.WriteFile("/output/report.json", []byte(reportJSON), 0644)

	// 4. Return stdout confirmation
	result := OutputResult{
		TotalFiles: total,
		ReportPath: "/output/report.json",
		Status:     "success",
	}
	resp, _ := json.Marshal(result)
	fmt.Print(string(resp))
}
```

### Compiling to WASI & Host HTTP Bindings

```bash
# Using TinyGo:
tinygo build -o textstats.wasm -target=wasi .

# Or using standard Go (1.24+):
GOOS=wasip1 GOARCH=wasm go build -o textstats.wasm .
```

Go skills that call `HttpGet(url)` via the auto-injected SDK import the host function `"vrax_sandbox"."http_get"`, which enforces manifest `capabilities.network` rules (`allowed_schemes`, `allowed_hosts`), redirect re-validation, a 2 MB response cap, and the active `airgap`/`local_lan`/`hybrid` perimeter.

---

## 3. The Autonomous Skill Loop: `vraxter-coder`

When a user requests a capability that Vraxter does not currently possess, the **CodeGen Engine** autonomously synthesizes it:

1. **Requirement Analysis**: Infers inputs, outputs, and storage constraints.
2. **Code Generation**: Generates Go, Rust, or Zig code with host bindings.
3. **Manifest Synthesis**: Authors a `skill-manifest/1` JSON specification (`[VRAX_MANIFEST]`) whose capabilities, IO paths, routing metadata, and limits are preserved into `<id>.manifest.json` while enforcing `Tier3Unverified` (fail-closed) governance.
4. **Hermetic Compilation**: Builds the binary inside a randomized `0700` temporary directory using `GOPROXY=off` and `GOSUMDB=off`.
5. **Dry-Run Validation Pipeline**: Instantiates Wazero and executes the binary in a zero-capability sandbox against `_vraxter_dry_run`. If the skill crashes or fails validation, both binary and manifest are immediately purged.
6. **Persistence & Hashing**: Enforces the 50 MB binary ceiling, records the SHA-256 hash in SQLite, and marks the skill `Tier3Unverified` (privileged capabilities require explicit operator trust via `vraxter skills trust <id>`).

---

## 4. Native IO & Sensory Skills

Vraxter also features native capabilities that hook directly into the Orchestrator without spawning a WASM sandbox:

### Native IO Skills (Workspace-Confined via `os.OpenRoot`)
- **`vraxter-read-file`**: High-speed reading of workspace files confined strictly inside the workspace root.
- **`vraxter-list-dir`**: Exploration of repository file trees confined to the workspace root.
- **`vraxter-patch-code`**: Precision line-targeted patching of workspace source files confined via `os.OpenRoot`.

### Multimodal Senses
- **`vraxter-see`**: Vision model integration for image analysis and UI critique.
- **`vraxter-hear`**: Local audio transcription of operator voice input.
- **`vraxter-talk`**: Text-to-speech engine targeting physical room audio sinks (`source_zone`).

---

## 5. Registering Skills Manually

> [!NOTE]
> **Offline First**: `vraxter skills inject` is the production pathway for loading local `.wasm` binaries and `skill-manifest/1` packages. Remote Skill Hub commands (`vraxter skills install` / `download`) are currently placeholder stubs. All injected skills default to `Tier3Unverified`; `source: "host"` mounts are blocked by default and forbidden in `airgap` mode.

Skills can be ingested through the CLI using multiple flexible pathways:

```bash
# 1. Ingest a standalone WASM binary (auto-generates standard Manifest V1):
vraxter skills inject ./bin/tool.wasm

# 2. Ingest an entire skill directory containing manifest.json and binary:
vraxter skills inject ./my-skill/

# 3. Ingest by pointing directly to an authored Manifest V1 file:
vraxter skills inject ./my-skill/manifest.json

# 4. Ingest binary with an explicit manifest override:
vraxter skills inject ./bin/tool.wasm --manifest ./my-manifest.json
```

### Verifying Integrity
```bash
# Cryptographically inspect an installed skill's SHA-256 hash:
vraxter skills inspect lab.textstats
```
