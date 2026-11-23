# Vraxter Critical-Grade Benchmark Report

- **Generated (UTC):** 2026-11-23T20:42:10Z
- **Platform:** windows/amd64 (6 CPUs, go1.26.3, CGO_ENABLED=0)

## Summary of the 7 Critical-Grade Benchmarks

| # | Benchmark Scenario | Key Quantitative Result | Status |
| :-: | :--- | :--- | :-: |
| **1** | **Zero-Egress Guarantee** (`Bench01_ZeroEgressAirgap`) | **20 / 20** outbound attempts blocked (**100.00%** block rate, **0** permitted conns, **0** OTel egress) | PASS |
| **2** | **WASM Cold & Warm Start vs OS Subprocess** (`Bench02_WASMColdWarmStart`) | Warm p50: **2.072 ms** (2072.40 µs, 233390 B/op) \| Cold p50: **3.985 ms** \| OS Subprocess p50: **21.968 ms** (**10.60x** speedup) | PASS |
| **3** | **Deterministic DoS Containment** (`Bench03_DeterministicDoSContainment`) | CPU Loop trapped at **500/500** fuel in **53.578 ms** \| Mem Bomb (32MB > 2MB) trapped in **62.250 ms** \| Disk violation blocked in **63.569 ms** \| **12/12** concurrent legit skills passed | PASS |
| **4** | **Execution & Canonicalization Determinism** (`Bench04_ExecutionDeterminism`) | **50** iterations: **1** unique output hash, **1** WASM hash, **1** manifest hash, **1** ledger hash (**100.0%** bit-for-bit determinism) | PASS |
| **5** | **Crash-Safety & Abrupt Kill Recovery** (`Bench05_CrashSafetyRecovery`) | **25** `Process.Kill()` cycles: **0** corrupt atomic files, **0** orphan temp files, **25/25** `PRAGMA integrity_check == ok`, **5/5** sealed DB cycles | PASS |
| **6** | **Offline Skill Synthesis & Signed Pipeline** (`Bench06_OfflineSkillPipeline`) | Compile (`GOPROXY=off`=true): **453.186 ms** \| Static Verify: **963.932 ms** \| Ed25519 Sign: **37.454 ms** \| Register: **992.658 ms** \| Sandbox Exec: **946.147 ms** \| Total: **3393.889 ms** | PASS |
| **7** | **Immutable Flight Recorder Overhead** (`Bench07_ImmutableAuditOverhead`) | **1000** signed hash-chained events: Write p50 **0.744 ms** (**1265.1 ops/sec**) \| Full `VerifyChain()` (**1000** events): **71.224 ms** (**14040.1 events/sec**) | PASS |
