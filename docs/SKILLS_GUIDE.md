# Vraxter Skills Development Guide (`v1.0.0`)

A **Skill** in Vraxter is a deterministic, capability-bounded WebAssembly (WASI) executable running inside the pure-Go **Wazero** sandbox. All skills are governed by `skill-manifest/1` (`schemas/manifest.schema.json`), validated against `io.input.schema` prior to execution, metered by deterministic **WASM CPU Fuel**, bounded by **QuotaFS**, and cryptographically authenticated using **Ed25519** signatures.

---

## 1. Skill Manifest V1 Specification (`skill-manifest/1`)

Every skill declares its exact IO contract, permissions, governance rules, and resource ceilings in `manifest.json` conforming to [schemas/manifest.schema.json](../schemas/manifest.schema.json). Any omitted capability is **denied by default**.

### Complete Manifest Example

```json
{
  "$schema": "https://vraxter.io/schemas/skill-manifest.v1.json",
  "manifest_version": "skill-manifest/1",
  "id": "vrx.telemetry.analyzer",
  "name": "Turbine Telemetry Analyzer",
  "version": "1.0.0",
  "description": "Deterministic read-only WASI analyzer for sensor telemetry batches.",
  "agent": {
    "risk": "low",
    "approval": "never",
    "allow_daemon": false
  },
  "module": {
    "file": "vrx-telemetry-analyzer.wasm",
    "sha256": "84a3418c67bee2d03b306b973ef4d73cf0c5c4e365cb9dc8727d2c3df478fb12",
    "abi": "wasip1"
  },
  "routing": {
    "keywords": ["telemetry", "turbine", "anomaly"],
    "tags": ["aerospace", "airgap", "deterministic"]
  },
  "io": {
    "input": {
      "via": "stdin",
      "format": "json",
      "schema": {
        "type": "object",
        "additionalProperties": false,
        "required": ["batch_file"],
        "properties": {
          "batch_file": {
            "type": "string",
            "minLength": 1,
            "maxLength": 255
          }
        }
      }
    },
    "output": {
      "via": "stdout",
      "format": "json"
    }
  },
  "capabilities": {
    "fs": {
      "mounts": [
        {
          "guest": "/input",
          "source": "input",
          "mode": "ro",
          "allow_extensions": [".jsonl", ".json"],
          "max_file_mb": 10
        },
        {
          "guest": "/output",
          "source": "output",
          "mode": "rw",
          "subpath": "reports",
          "max_total_mb": 5,
          "max_files": 20,
          "allow_delete": false
        }
      ]
    },
    "network": {
      "mode": "none"
    },
    "clock": {
      "wall": false,
      "monotonic": true
    },
    "random": false
  },
  "limits": {
    "memory_mb": 64,
    "max_runtime_ms": 5000,
    "max_cpu_fuel": 25000000
  }
}
```

### Manifest Sections Reference

| Section | Enforced Runtime Semantics |
| :--- | :--- |
| **`manifest_version`** | Must be `"skill-manifest/1"`. |
| **`agent`** | `risk` (`"low"`, `"medium"`, `"high"`, `"critical"`), `approval` (`"never"`, `"always"`, `"on_risk"`, `"conditional"`), and `allow_daemon` (`bool`, default `false` — required opt-in before detached background execution, always HITL-gated). |
| **`module`** | Relative `.wasm` path, expected 64-char hex `sha256`, and `abi` (`"wasip1"`, `"wasip2"`, `"generic"`). |
| **`io.input`** | Passes JSON-RPC 2.0 request via `stdin`. If `schema` is a JSON Schema Draft-07 object, `Runner.Execute` validates `params` **fail-closed** before instantiating WASM and audits rejections as `POLICY_EVALUATED (DENY)`. |
| **`io.output`** | Captures bounded `stdout` (2 MB cap) or reads confined `file` path via `os.OpenRoot`. |
| **`capabilities.fs`** | Virtualized storage pools (`workspace`, `input`, `output`, `skill_data`, `ephemeral`). `source: "host"` is blocked by default and forbidden in `airgap`. Every mount is wrapped in `QuotaFS` enforcing `allow_extensions`, `max_file_mb`, `max_total_mb`, `max_files`, and `allow_delete`. Untrusted params (`input_dir`, `output_dir`, etc.) cannot override host mount directories. |
| **`capabilities.network`** | Default `mode: "none"`. `fetch` (`vrax_sandbox.http_get`) enforces `allowed_hosts`, `allowed_ports`, `allowed_schemes`, redirect SSRF checks, 2 MB cap, and active enclave isolation mode (`airgap`/`local_lan`/`hybrid`). |
| **`limits`** | `memory_mb` (1–2048 MB), `max_runtime_ms` (100–300000 ms), and `max_cpu_fuel` (deterministic basic-block/loop fuel trap). |

---

## 2. Authoring & Compiling a WASI Skill (Go)

Skills receive a JSON-RPC 2.0 request envelope on `stdin` (`{"jsonrpc":"2.0","method":"execute","params":{...},"id":"..."}`) and write structured output to `stdout` (or a declared `/output` file):

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type SkillRequest struct {
	Params map[string]interface{} `json:"params"`
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
		os.Exit(1)
	}
	var req SkillRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		fmt.Fprintf(os.Stderr, "invalid JSON-RPC request: %v\n", err)
		os.Exit(1)
	}

	out := map[string]interface{}{
		"status": "ok",
		"params": req.Params,
	}
	enc, _ := json.Marshal(out)
	fmt.Print(string(enc))
}
```

Compile cleanly to WASI Preview 1 with pure Go:

```bash
GOOS=wasip1 GOARCH=wasm go build -trimpath -o my-skill.wasm .
```

See real production-grade WASI examples in [demos/01-aerospace-telemetry-airgap/skill/main.go](../demos/01-aerospace-telemetry-airgap/skill/main.go) and [demos/02-sovereign-soc-triage/skill/main.go](../demos/02-sovereign-soc-triage/skill/main.go).

---

## 3. Cryptographic Supply Chain: Ed25519 Signing & Verification

Vraxter determines trust **exclusively** via Ed25519 signatures verified against local `trusted_signers/` and explicit local operator policy (there is no external app-store reputation scoring).

The domain-separated signing payload is:
```text
"VRAXTER-SKILL-V1\n" + sha256_hex(wasmBytes) + "\n" + sha256_hex(canonicalManifestBytes)
```

### Offline PKI & Injection Workflow

```bash
# 1. Generate an Ed25519 signing keypair (0600 private key, 0644 public key)
vraxter skill keygen --name "org-release-signer" --out ./keys

# 2. Sign the .wasm binary and manifest.json -> produces my-skill.sig
vraxter skill sign \
  --wasm ./my-skill.wasm \
  --manifest ./manifest.json \
  --key ./keys/org-release-signer.key

# 3. Register the public key in local trusted_signers/ (Tier 1 = Official, Tier 2 = Verified)
vraxter skill trust add \
  --pub ./keys/org-release-signer.pub \
  --name "org-release-signer" \
  --tier 1

# 4. Verify offline (works on clean auditor hosts with --pub and zero AppDir side effects)
vraxter skill verify \
  --wasm ./my-skill.wasm \
  --manifest ./manifest.json \
  --sig ./my-skill.sig \
  --pub ./keys/org-release-signer.pub

# 5. Inject the signed artifact into the local enclave daemon
vraxter skills inject ./my-skill.wasm --manifest ./manifest.json

# 6. Inspect SHA-256 integrity of an installed skill
vraxter skills inspect my-skill
```

When `require_signed_skills=true` (`vraxter --require-signed-skills` or `vraxter config set require_signed_skills true`):
- Any unsigned or tampered `.wasm` / `manifest.json` fails closed at both install time and execution time.
- Dynamic runtime code generation (`vraxter-coder`) is disabled automatically (`allow_runtime_codegen=false`) unless explicitly overridden.

---

## 4. Native Confined Skills

Vraxter also provides built-in workspace-confined native skills:
- **`vraxter-read-file`** / **`vraxter-list-dir`**: Strictly confined to the workspace root via Go's `os.OpenRoot`.
- **`vraxter-patch-code`**: Confined via `os.OpenRoot`, always gated by HITL approval, and logged to `audit_ledger`.
- **`vraxter-see`** / **`vraxter-hear`**: Confined via `os.OpenRoot`, enforced 10 MB file cap, magic-byte validation, and audit-logged.
- **`vraxter-coder`**: Hermetic offline WASI compiler (`GOPROXY=off`, `GOSUMDB=off`, `CGO_ENABLED=0`, sanitized build environment, randomized `0700` temp build dirs, zero-capability dry-run check). Disabled automatically when `require_signed_skills=true`.
