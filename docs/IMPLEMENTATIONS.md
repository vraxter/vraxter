# Powers & Implementations

Vraxter activates different powers based on your implementation.
You can configure your active world during the setup wizard or manually via `~/.vraxter/settings.json`.

| Power | Core | Habitat | Facility | Enterprise | Sovereign |
|-------|------|---------|----------|------------|-----------|
| WASM Skills | ✅ | ✅ | ✅ | ✅ | ✅ |
| Spatial Concurrency | — | ✅ | ✅ | — | ✅ |
| Skill Approval | — | — | ✅ | ✅ | ✅ |
| Air-Gapped | — | — | — | ✅ | ✅ |

⚙️ = configurable manually

## Compilation Modes

Vraxter's codebase is internally strictly separated using Go's build tags. This allows us to maintain a completely unified repository without bleeding Enterprise code into a Home setup, or vice-versa.

### 1. The Universal Binary
To compile a single binary containing all worlds (where Vraxter dynamically routes based on the config file), compile with the global `all_worlds` tag:

```bash
go build -tags "all_worlds" -o bin/vraxter ./cmd/vraxter
```

### 2. Implementation-Specific Builds
If you want to compile a binary perfectly locked to a single implementation (e.g., preventing any Enterprise RBAC logic from even existing in the compiled binary), you use the specific tag:

```bash
# Compiles the Habitat assistant mode ONLY.
go build -tags "habitat" -o bin/vraxter-habitat ./cmd/vraxter

# Compiles the Enterprise air-gapped mode ONLY.
go build -tags "enterprise" -o bin/vraxter-ent ./cmd/vraxter
```
