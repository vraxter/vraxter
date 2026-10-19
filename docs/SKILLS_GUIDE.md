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

### Compiling to WASI

```bash
# Using TinyGo:
tinygo build -o textstats.wasm -target=wasi .

# Or using standard Go (1.21+):
GOOS=wasip1 GOARCH=wasm go build -o textstats.wasm .
```

---

## 3. The Autonomous Skill Loop: `vraxter-coder`

When a user requests a capability that Vraxter does not currently possess, the **CodeGen Engine** autonomously synthesizes it:

1. **Requirement Analysis**: Infers inputs, outputs, and storage constraints.
2. **Code Generation**: Generates Go, Rust, or Zig code with host bindings.
3. **Manifest Synthesis**: Automatically authors an official `skill-manifest/1` JSON specification with least-privilege capabilities.
4. **Hermetic Compilation**: Builds the binary using `GOPROXY=off` and `GOSUMDB=off`.
5. **Dry-Run Validation Pipeline**: Instantiates Wazero and executes the binary against simulated inputs in an isolated test harness. If the skill crashes, leaks memory, or fails validation, the binary is immediately purged.
6. **Persistence & Hashing**: Registers the verified skill and records its SHA-256 hash in the SQLite repository.

---

## 4. Native IO & Sensory Skills

Vraxter also features native capabilities that hook directly into the Orchestrator without spawning a WASM sandbox:

### Native IO Skills
- **`vraxter-read-file`**: High-speed, sandboxed reading of local workspace files into context.
- **`vraxter-list-dir`**: Recursive exploration of repository file trees.
- **`vraxter-patch-code`**: Precision line-targeted patching of source files.

### Multimodal Senses
- **`vraxter-see`**: Vision model integration for image analysis and UI critique.
- **`vraxter-hear`**: Local audio transcription of operator voice input.
- **`vraxter-talk`**: Text-to-speech engine targeting physical room audio sinks (`source_zone`).

---

## 5. Registering Skills Manually

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
