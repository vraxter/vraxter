# Vraxter Security & Isolation (Vraxter Shield)

Vraxter is designed for high-stakes engineering environments where code execution safety is non-negotiable. The **Vraxter Shield** model provides multiple layers of defense.


## Capability-Based Security

Vraxter uses a **Principle of Least Privilege** for all tool execution.

### 1. WASM Sandbox Isolation
Tools compiled to WASM run in the **Wazero** runtime, which implements a zero-trust architecture:
- **No Syscalls**: The WASM module cannot invoke OS-level syscalls directly.
- **Resource Constraints**: Strict memory limits (default 128MB) and CPU timeout bounds (60 seconds) prevent Denial-of-Service attacks.
- **FS Virtualization**: The sandbox only "sees" files and directories that the user has explicitly mounted.

### 2. Checksum Verification
Every skill registered in Vraxter includes a **SHA-256 Checksum**.
- On every execution, Vraxter hashes the binary and compares it to the database record.
- If the hashes do not match, Vraxter triggers a **Security Alert** and halts execution to prevent binary hijacking.


## Credential Management

- **AES-256-GCM Encryption**: All LLM Provider API keys are encrypted before being saved to the SQLite database.
- **Key Location**: The master encryption key is kept in a separate file (usually `~/.config/vraxter/vrax.key`) with restricted OS permissions (0600).
- **In-Memory Hygiene**: Sensitive tokens are purged from memory once the gRPC stream connection is closed.


## Trust Tiers

Vraxter classifies tools into trust tiers:
- **Tier 1 (Official)**: Tools signed by PatagonicRune. Always trusted.
- **Tier 2 (Trusted Community)**: Tools with high reputation and usage.
- **Tier 3 (Unverified)**: Manually registered scripts. These require explicit user `trust` via the CLI before they can run outside the narrowest sandbox.


## Data Sovereignty

- **Local Persistence**: All chat history and embeddings are stored in a local SQLite file.
- **No Telemetry**: Vraxter does not send telemetry or usage data back to PatagonicRune.
- **Provider Choice**: Users can switch to local providers like **Ollama** to ensure that not even the query text leaves their machine.
