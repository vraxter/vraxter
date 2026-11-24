# Vraxter Critical-Grade Benchmark Report

- **Generated (UTC):** 2026-11-24T00:05:26Z
- **Platform:** windows/amd64 (6 CPUs, go1.26.3, CGO_ENABLED=0)

## Summary of the 7 Critical-Grade Benchmarks

| # | Benchmark Scenario | Key Quantitative Result | Status |
| :-: | :--- | :--- | :-: |
| **1** | **Zero-Egress Guarantee** (`Bench01_ZeroEgressAirgap`) | **20 / 20** outbound attempts blocked (**100.00%** block rate, **0** permitted conns, **0** OTel egress) | PASS |
| **2** | **WASM Cold & Warm Start vs OS Subprocess** (`Bench02_WASMColdWarmStart`) | Warm p50: **1.995 ms** (1995.00 µs, 240158 B/op) \| Cold p50: **39.348 ms** \| OS Subprocess p50: **41.588 ms** (**20.85x** speedup) | PASS |
| **3** | **Deterministic DoS Containment** (`Bench03_DeterministicDoSContainment`) | CPU Loop trapped at **500/500** fuel in **313.365 ms** \| Mem Bomb (32MB > 2MB) trapped in **407.137 ms** \| Disk violation blocked in **19.702 ms** \| **12/12** concurrent legit skills passed | PASS |
| **4** | **Execution & Canonicalization Determinism** (`Bench04_ExecutionDeterminism`) | **50** iterations: **1** unique output hash, **1** WASM hash, **1** manifest hash, **1** ledger hash (**100.0%** bit-for-bit determinism) | PASS |
| **5** | **Crash-Safety & Abrupt Kill Recovery** (`Bench05_CrashSafetyRecovery`) | **25** `Process.Kill()` cycles: **0** corrupt atomic files, **0** orphan temp files, **25/25** `PRAGMA integrity_check == ok`, **5/5** sealed DB cycles | PASS |
| **6** | **Offline Skill Synthesis & Signed Pipeline** (`Bench06_OfflineSkillPipeline`) | Compile (`GOPROXY=off`=true): **1718.662 ms** \| Static Verify: **2598.809 ms** \| Ed25519 Sign: **125.728 ms** \| Register: **2675.784 ms** \| Sandbox Exec: **1917.282 ms** \| Total: **9043.537 ms** | PASS |
| **7** | **Immutable Flight Recorder Overhead** (`Bench07_ImmutableAuditOverhead`) | **1000** signed hash-chained events: Write p50 **0.997 ms** (**638.2 ops/sec**) \| Full `VerifyChain()` (**1000** events): **173.518 ms** (**5763.1 events/sec**) | PASS |
