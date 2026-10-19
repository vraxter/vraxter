# Powers & Implementations

Vraxter activates different powers based on your operational implementation and environment.
You can configure your active world during the setup wizard, via CLI commands, or manually via `~/.config/vraxter/settings.json` (or `~/.vraxter/settings.json`).

| Power | Core | Habitat | Facility | Enterprise | Sovereign |
|---|:---:|:---:|:---:|:---:|:---:|
| **WASM Skill Sandbox (V1)** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Spatial Awareness & Concurrency** | — | ✅ | ✅ | — | ✅ |
| **HITL Skill Approval Gate** | — | — | ✅ | ✅ | ✅ |
| **Air-Gapped Isolation Mode** | — | — | — | ✅ | ✅ |

### Configuration Schema (`settings.json`)

```json
{
  "implementation": "enterprise",
  "isolation_mode": "airgap",
  "require_skill_approval": true,
  "hub_url": "https://hub.vraxter.com",
  "whitelisted_ips": ["192.168.1.0/24"]
}
```

- **`implementation`**: Active feature profile (`core`, `habitat`, `facility`, `enterprise`, `sovereign`, `custom`).
- **`isolation_mode`**: Network perimeter policy (`airgap` [zero-egress to WAN, private LAN allowed], `local_lan` [loopback + private subnets], `hybrid` [WAN enabled]).
- **`require_skill_approval`**: When `true`, all skill executions require human-in-the-loop authorization via the Decision Bridge.
- **`whitelisted_ips`**: Specific IP addresses or CIDR blocks permitted regardless of perimeter defaults.

### CLI Configuration Commands

```bash
# Set active implementation world
vraxter config set implementation enterprise

# Set isolation mode
vraxter config set isolation_mode airgap

# Toggle skill approval gate
vraxter config set require_skill_approval true
```

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
