# Vraxter Critical-Grade Benchmark Report

- **Generated (UTC):** 2026-11-23T20:29:21Z
- **Platform:** windows/amd64 (6 CPUs, go1.26.3, CGO_ENABLED=0)

## Summary of the 7 Critical-Grade Benchmarks

| # | Benchmark Scenario | Key Quantitative Result | Status |
| :-: | :--- | :--- | :-: |
| **1** | **Zero-Egress Guarantee** (`Bench01_ZeroEgressAirgap`) | **20 / 20** outbound attempts blocked (**100.00%** block rate, **0** permitted conns, **0** OTel egress) | PASS |
| **2** | **WASM Cold & Warm Start vs OS Subprocess** (`Bench02_WASMColdWarmStart`) | Warm p50: **1.621 ms** (1620.90 µs, 227984 B/op) \| Cold p50: **2.157 ms** \| OS Subprocess p50: **12.598 ms** (**7.77x** speedup) | PASS |
| **3** | **Deterministic DoS Containment** (`Bench03_DeterministicDoSContainment`) | CPU Loop trapped at **500/500** fuel in **8.332 ms** \| Mem Bomb (32MB > 2MB) trapped in **12.942 ms** \| Disk violation blocked in **8.487 ms** \| **12/12** concurrent legit skills passed | PASS |
| **4** | **Execution & Canonicalization Determinism** (`Bench04_ExecutionDeterminism`) | **50** iterations: **1** unique output hash, **1** WASM hash, **1** manifest hash, **1** ledger hash (**100.0%** bit-for-bit determinism) | PASS |
| **5** | **Crash-Safety & Abrupt Kill Recovery** (`Bench05_CrashSafetyRecovery`) | **25** `Process.Kill()` cycles: **0** corrupt atomic files, **0** orphan temp files, **25/25** `PRAGMA integrity_check == ok`, **5/5** sealed DB cycles | PASS |
| **6** | **Offline Skill Synthesis & Signed Pipeline** (`Bench06_OfflineSkillPipeline`) | Compile (`GOPROXY=off`=true): **360.228 ms** \| Static Verify: **805.030 ms** \| Ed25519 Sign: **22.331 ms** \| Register: **851.748 ms** \| Sandbox Exec: **884.735 ms** \| Total: **2924.073 ms** | PASS |
| **7** | **Immutable Flight Recorder Overhead** (`Bench07_ImmutableAuditOverhead`) | **1000** signed hash-chained events: Write p50 **0.584 ms** (**1388.0 ops/sec**) \| Full `VerifyChain()` (**1000** events): **70.597 ms** (**14164.9 events/sec**) | PASS |
