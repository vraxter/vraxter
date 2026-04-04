# 👁️ VRAXTER

>**The Sovereign Agent Engine.**
>
> High-performance, local-first orchestrator built in Go.
>
> Engineered for tactical autonomy and neural reasoning.

## PatagonicRune | Logic. Power. Autonomy.

Vraxter is not a chatbot. It is a persistent **Daemon** and **Execution Engine** designed to bridge the gap between high-level LLM reasoning and native system execution. Built by **PatagonicRune**, it focuses on three core pillars:

* **Sovereignty:** Your data, your keys, your local execution.
* **Resilience:** Multi-model priority failover (Local + Cloud).
* **Action:** A native "Intent Router" designed to trigger real-world skills, not just generate text.


## 🛠️ Architecture Overview

Vraxter operates as a **Headless Daemon** (`vraxterd`) with a high-speed gRPC interface, allowing for multiple frontends (CLI, Web, Voice) to share a single "source of truth" and persistent memory.



### Core Features (Phase 1: The Skeleton)
* **Dual-Core Brain:** Native Go engine with SQLite-backed long-term memory.
* **Failover Logic:** Automatic model switching based on priority (0: Fav, 1: Fallback, etc.).
* **Agnostic Provider:** Support for OpenAI, Anthropic, and Local LLMs (Ollama/Whisper.cpp).
* **Tactical TUI:** A sophisticated terminal interface built with Bubble Tea for real-time monitoring.


## 🚀 Getting Started

### 1. Requirements
* Go 1.21+
* SQLite3

### 2. Installation
```bash
git clone [https://github.com/PatagonicRune/vraxter.git](https://github.com/PatagonicRune/vraxter.git)
cd vraxter
go mod download
```

### 3. Initialize the core
```bash
go run cmd/vraxter/main.go
```

## The Intent Pipeline

Vraxter doesn't just "talk". Every input passes through a Tactical Router that decides the best course of action:

* Direct Match: Fast-path execution for registered Skills.
* Intent Classification: Neural analysis to determine the Operator's goal.
* Neural Reasoning: LLM-driven response for complex queries.

## Aesthetic & Status

Vraxter uses a visual language of light to communicate its state:

* Gold (Idle/Stable): Ready for orders.
* Amber (Processing): Executing neural reasoning.
* Crimson (Alert): System error or unauthorized access detected.

## License

Distributed under the MIT License. Built with precision in the Patagonia.

Developed by [Diego Rodriguez](https://github.com/drcode-rune) from [Patagonic Rune](https://github.com/PatagonicRune)