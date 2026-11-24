// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

const (
	// DefaultMaxCPUFuel is the default deterministic CPU instruction/basic-block fuel budget
	// per WASM execution when manifest.Limits.MaxCPUFuel is not specified.
	DefaultMaxCPUFuel int64 = 50_000_000

	// FuelRemainingExportName is the exported mutable i64 global tracking remaining CPU fuel.
	FuelRemainingExportName = "__vraxter_fuel_remaining"
	// FuelExhaustedExportName is the exported mutable i32 global set to 1 right before trapping on fuel exhaustion.
	FuelExhaustedExportName = "__vraxter_fuel_exhausted"
)

// ErrCPUFuelExhausted is returned when a WASM module exceeds its deterministic CPU fuel budget.
var ErrCPUFuelExhausted = errors.New("VRAXTER SHIELD: WASM CPU fuel exhausted (deterministic instruction budget exceeded)")

// InstrumentWASMWithFuel transforms a WebAssembly binary by injecting deterministic CPU fuel
// metering at every function entry and every loop header (opcode 0x03).
// It appends two mutable globals (__vraxter_fuel_remaining: i64 and __vraxter_fuel_exhausted: i32)
// at the end of the Global section (preserving all existing global, function, type, and branch indices)
// and exports them so the host Runner can configure and inspect exact fuel consumption per invocation.
func InstrumentWASMWithFuel(wasmBytes []byte, initialFuel int64) ([]byte, error) {
	if len(wasmBytes) < 8 || string(wasmBytes[:4]) != "\x00asm" {
		return nil, fmt.Errorf("invalid WASM header")
	}
	if initialFuel <= 0 {
		initialFuel = DefaultMaxCPUFuel
	}

	// Avoid double-instrumenting if already instrumented
	if bytes.Contains(wasmBytes, []byte(FuelRemainingExportName)) {
		return wasmBytes, nil
	}

	type rawSection struct {
		id      byte
		payload []byte
	}

	var sections []rawSection
	pos := 8
	for pos < len(wasmBytes) {
		secID := wasmBytes[pos]
		pos++
		secLen, n, err := readULEB128(wasmBytes[pos:])
		if err != nil {
			return nil, fmt.Errorf("corrupt WASM section length: %w", err)
		}
		pos += n
		if pos+int(secLen) > len(wasmBytes) {
			return nil, fmt.Errorf("WASM section %d extends past EOF", secID)
		}
		sections = append(sections, rawSection{
			id:      secID,
			payload: wasmBytes[pos : pos+int(secLen)],
		})
		pos += int(secLen)
	}

	// 1. Count imported globals (Section 2) and defined globals (Section 6)
	var numImportedGlobals uint32
	var numDefinedGlobals uint32
	hasGlobalSec := false
	hasExportSec := false

	for _, sec := range sections {
		switch sec.id {
		case 2: // Import section
			ig, err := countImportedGlobals(sec.payload)
			if err != nil {
				return nil, err
			}
			numImportedGlobals = ig
		case 6: // Global section
			hasGlobalSec = true
			cnt, _, err := readULEB128(sec.payload)
			if err != nil {
				return nil, fmt.Errorf("corrupt global section count: %w", err)
			}
			numDefinedGlobals = cnt
		case 7: // Export section
			hasExportSec = true
		}
	}

	fuelRemIdx := numImportedGlobals + numDefinedGlobals
	fuelExhIdx := fuelRemIdx + 1

	// Build the deterministic fuel check bytecode sequence injected at function entry and loop headers:
	//   global.get $fuelRemIdx
	//   i64.const 1
	//   i64.sub
	//   global.set $fuelRemIdx
	//   global.get $fuelRemIdx
	//   i64.const 0
	//   i64.lt_s
	//   if (empty blocktype 0x40)
	//     i32.const 1
	//     global.set $fuelExhIdx
	//     unreachable
	//   end
	fuelCheckSeq := buildFuelCheckSequence(fuelRemIdx, fuelExhIdx)

	// Prepare the two new global definitions to append to Section 6
	newGlobalsBytes := buildFuelGlobalEntries(initialFuel)
	// Prepare the two new export entries to append to Section 7
	newExportsBytes := buildFuelExportEntries(fuelRemIdx, fuelExhIdx)

	var out bytes.Buffer
	out.Write(wasmBytes[:8]) // Magic + Version

	insertedGlobals := false
	insertedExports := false

	emitGlobalSection := func(existingPayload []byte) error {
		insertedGlobals = true
		if len(existingPayload) == 0 {
			var p bytes.Buffer
			writeULEB128(&p, 2)
			p.Write(newGlobalsBytes)
			writeSection(&out, 6, p.Bytes())
			return nil
		}
		cnt, n, err := readULEB128(existingPayload)
		if err != nil {
			return err
		}
		var p bytes.Buffer
		writeULEB128(&p, cnt+2)
		p.Write(existingPayload[n:])
		p.Write(newGlobalsBytes)
		writeSection(&out, 6, p.Bytes())
		return nil
	}

	emitExportSection := func(existingPayload []byte) error {
		insertedExports = true
		if len(existingPayload) == 0 {
			var p bytes.Buffer
			writeULEB128(&p, 2)
			p.Write(newExportsBytes)
			writeSection(&out, 7, p.Bytes())
			return nil
		}
		cnt, n, err := readULEB128(existingPayload)
		if err != nil {
			return err
		}
		var p bytes.Buffer
		writeULEB128(&p, cnt+2)
		p.Write(existingPayload[n:])
		p.Write(newExportsBytes)
		writeSection(&out, 7, p.Bytes())
		return nil
	}

	for _, sec := range sections {
		// In WASM binary order, Section 6 (Global) must come before Sections 7, 8, 9, 12, 10, 11.
		if !hasGlobalSec && !insertedGlobals && (sec.id >= 7 || sec.id == 10 || sec.id == 11 || sec.id == 12) {
			if err := emitGlobalSection(nil); err != nil {
				return nil, err
			}
		}
		// Section 7 (Export) must come after Section 6 and before Sections 8, 9, 12, 10, 11.
		if !hasExportSec && !insertedExports && (sec.id >= 8 || sec.id == 10 || sec.id == 11 || sec.id == 12) {
			if err := emitExportSection(nil); err != nil {
				return nil, err
			}
		}

		switch sec.id {
		case 6:
			if err := emitGlobalSection(sec.payload); err != nil {
				return nil, err
			}
			if !hasExportSec && !insertedExports {
				if err := emitExportSection(nil); err != nil {
					return nil, err
				}
			}
		case 7:
			if err := emitExportSection(sec.payload); err != nil {
				return nil, err
			}
		case 10: // Code section
			instrumentedCode, err := instrumentCodeSection(sec.payload, fuelCheckSeq)
			if err != nil {
				return nil, err
			}
			writeSection(&out, 10, instrumentedCode)
		default:
			writeSection(&out, sec.id, sec.payload)
		}
	}

	return out.Bytes(), nil
}

// SetModuleFuel initializes or resets the CPU fuel budget on an instantiated WASM module prior to invoking _start.
func SetModuleFuel(mod api.Module, maxFuel int64) {
	if mod == nil {
		return
	}
	if maxFuel <= 0 {
		maxFuel = DefaultMaxCPUFuel
	}
	if g := mod.ExportedGlobal(FuelRemainingExportName); g != nil {
		if mg, ok := g.(api.MutableGlobal); ok {
			mg.Set(uint64(maxFuel))
		}
	}
	if g := mod.ExportedGlobal(FuelExhaustedExportName); g != nil {
		if mg, ok := g.(api.MutableGlobal); ok {
			mg.Set(0)
		}
	}
}

// IsModuleFuelExhausted returns true if the instantiated WASM module trapped due to exhausting its CPU fuel budget.
func IsModuleFuelExhausted(mod api.Module) bool {
	if mod == nil {
		return false
	}
	if g := mod.ExportedGlobal(FuelExhaustedExportName); g != nil {
		if g.Get() != 0 {
			return true
		}
	}
	if g := mod.ExportedGlobal(FuelRemainingExportName); g != nil {
		if int64(g.Get()) < 0 {
			return true
		}
	}
	return false
}

// GetModuleFuelConsumed returns the exact number of CPU fuel units consumed by the module during execution.
func GetModuleFuelConsumed(mod api.Module, initialFuel int64) int64 {
	if mod == nil {
		return 0
	}
	if IsModuleFuelExhausted(mod) {
		return initialFuel
	}
	if g := mod.ExportedGlobal(FuelRemainingExportName); g != nil {
		rem := int64(g.Get())
		consumed := initialFuel - rem
		if consumed < 0 {
			return 0
		}
		return consumed
	}
	return 0
}

func buildFuelCheckSequence(fuelRemIdx, fuelExhIdx uint32) []byte {
	var b bytes.Buffer
	// global.get $fuelRemIdx
	b.WriteByte(0x23)
	writeULEB128(&b, fuelRemIdx)
	// i64.const 1
	b.WriteByte(0x42)
	b.WriteByte(0x01)
	// i64.sub
	b.WriteByte(0x7d)
	// global.set $fuelRemIdx
	b.WriteByte(0x24)
	writeULEB128(&b, fuelRemIdx)
	// global.get $fuelRemIdx
	b.WriteByte(0x23)
	writeULEB128(&b, fuelRemIdx)
	// i64.const 0
	b.WriteByte(0x42)
	b.WriteByte(0x00)
	// i64.lt_s
	b.WriteByte(0x53)
	// if (empty blocktype 0x40)
	b.WriteByte(0x04)
	b.WriteByte(0x40)
	// i32.const 1
	b.WriteByte(0x41)
	b.WriteByte(0x01)
	// global.set $fuelExhIdx
	b.WriteByte(0x24)
	writeULEB128(&b, fuelExhIdx)
	// unreachable
	b.WriteByte(0x00)
	// end
	b.WriteByte(0x0b)
	return b.Bytes()
}

func buildFuelGlobalEntries(initialFuel int64) []byte {
	var b bytes.Buffer
	// Global 1: (mut i64) (i64.const initialFuel)
	b.WriteByte(0x7e) // i64
	b.WriteByte(0x01) // mutable
	b.WriteByte(0x42) // i64.const
	writeSLEB128(&b, initialFuel)
	b.WriteByte(0x0b) // end

	// Global 2: (mut i32) (i32.const 0)
	b.WriteByte(0x7f) // i32
	b.WriteByte(0x01) // mutable
	b.WriteByte(0x41) // i32.const
	b.WriteByte(0x00) // 0
	b.WriteByte(0x0b) // end
	return b.Bytes()
}

func buildFuelExportEntries(fuelRemIdx, fuelExhIdx uint32) []byte {
	var b bytes.Buffer
	// Export 1: FuelRemainingExportName (kind 0x03 = global)
	writeULEB128(&b, uint32(len(FuelRemainingExportName)))
	b.WriteString(FuelRemainingExportName)
	b.WriteByte(0x03)
	writeULEB128(&b, fuelRemIdx)

	// Export 2: FuelExhaustedExportName (kind 0x03 = global)
	writeULEB128(&b, uint32(len(FuelExhaustedExportName)))
	b.WriteString(FuelExhaustedExportName)
	b.WriteByte(0x03)
	writeULEB128(&b, fuelExhIdx)
	return b.Bytes()
}

func countImportedGlobals(payload []byte) (uint32, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	count, pos, err := readULEB128(payload)
	if err != nil {
		return 0, err
	}
	var globals uint32
	for i := uint32(0); i < count; i++ {
		// module name
		modLen, n, err := readULEB128(payload[pos:])
		if err != nil {
			return 0, err
		}
		pos += n + int(modLen)
		// field name
		nameLen, n, err := readULEB128(payload[pos:])
		if err != nil {
			return 0, err
		}
		pos += n + int(nameLen)
		if pos >= len(payload) {
			return 0, fmt.Errorf("unexpected EOF in import section")
		}
		kind := payload[pos]
		pos++
		switch kind {
		case 0x00: // func import (type_idx: u32)
			_, n, err := readULEB128(payload[pos:])
			if err != nil {
				return 0, err
			}
			pos += n
		case 0x01: // table import (reftype: 1b, limits)
			pos++ // elemtype
			n, err := skipLimits(payload[pos:])
			if err != nil {
				return 0, err
			}
			pos += n
		case 0x02: // mem import (limits)
			n, err := skipLimits(payload[pos:])
			if err != nil {
				return 0, err
			}
			pos += n
		case 0x03: // global import (valtype: 1b, mut: 1b)
			pos += 2
			globals++
		default:
			return 0, fmt.Errorf("unsupported import kind 0x%02x", kind)
		}
	}
	return globals, nil
}

func skipLimits(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("unexpected EOF in limits")
	}
	flags := b[0]
	pos := 1
	_, n, err := readULEB128(b[pos:])
	if err != nil {
		return 0, err
	}
	pos += n
	if flags&0x01 != 0 {
		_, n, err := readULEB128(b[pos:])
		if err != nil {
			return 0, err
		}
		pos += n
	}
	return pos, nil
}

func instrumentCodeSection(payload []byte, fuelCheckSeq []byte) ([]byte, error) {
	funcCount, pos, err := readULEB128(payload)
	if err != nil {
		return nil, fmt.Errorf("corrupt code section count: %w", err)
	}

	var out bytes.Buffer
	writeULEB128(&out, funcCount)

	for i := uint32(0); i < funcCount; i++ {
		bodySize, n, err := readULEB128(payload[pos:])
		if err != nil {
			return nil, fmt.Errorf("corrupt function %d body size: %w", i, err)
		}
		pos += n
		if pos+int(bodySize) > len(payload) {
			return nil, fmt.Errorf("function %d body exceeds code section", i)
		}
		body := payload[pos : pos+int(bodySize)]
		pos += int(bodySize)

		instrumentedBody, err := instrumentFunctionBody(body, fuelCheckSeq)
		if err != nil {
			return nil, err
		}
		writeULEB128(&out, uint32(len(instrumentedBody)))
		out.Write(instrumentedBody)
	}

	return out.Bytes(), nil
}

func instrumentFunctionBody(body []byte, fuelCheckSeq []byte) ([]byte, error) {
	localDeclCount, pos, err := readULEB128(body)
	if err != nil {
		return nil, fmt.Errorf("corrupt local declaration count: %w", err)
	}
	for i := uint32(0); i < localDeclCount; i++ {
		_, n, err := readULEB128(body[pos:])
		if err != nil {
			return nil, fmt.Errorf("corrupt local declaration: %w", err)
		}
		pos += n + 1 // +1 for valtype byte
		if pos > len(body) {
			return nil, fmt.Errorf("local declarations exceed function body")
		}
	}

	localsPrefix := body[:pos]
	instrs := body[pos:]

	instrumentedInstrs, ok := instrumentInstructionStream(instrs, fuelCheckSeq)
	if !ok {
		// Safe fallback if an experimental opcode was encountered: still meter at function entry
		var fb bytes.Buffer
		fb.Write(localsPrefix)
		fb.Write(fuelCheckSeq)
		fb.Write(instrs)
		return fb.Bytes(), nil
	}

	var out bytes.Buffer
	out.Write(localsPrefix)
	out.Write(fuelCheckSeq)
	out.Write(instrumentedInstrs)
	return out.Bytes(), nil
}

// instrumentInstructionStream scans WebAssembly instructions and injects fuelCheckSeq
// immediately after every `loop <blocktype>` (0x03) header.
func instrumentInstructionStream(instrs []byte, fuelCheckSeq []byte) ([]byte, bool) {
	var out bytes.Buffer
	pos := 0
	for pos < len(instrs) {
		op := instrs[pos]
		start := pos
		pos++

		switch {
		case op == 0x00 || op == 0x01 || op == 0x05 || op == 0x0b || op == 0x0f || op == 0x1a || op == 0x1b || op == 0xd1:
			// Single-byte control / stack / ref.is_null instructions
			out.WriteByte(op)

		case op == 0x02 || op == 0x03 || op == 0x04:
			// block (0x02), loop (0x03), if (0x04) followed by blocktype (s33 LEB128)
			n, ok := skipSLEB128(instrs[pos:])
			if !ok {
				return nil, false
			}
			pos += n
			out.Write(instrs[start:pos])
			if op == 0x03 {
				// Inject fuel check at the start of every loop body!
				out.Write(fuelCheckSeq)
			}

		case op == 0x0c || op == 0x0d || op == 0x10 || (op >= 0x20 && op <= 0x26) || op == 0xd0 || op == 0xd2:
			// 1 ULEB128 immediate
			_, n, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n
			out.Write(instrs[start:pos])

		case op == 0x0e:
			// br_table: target_count (u32) + (target_count + 1) target labels (u32)
			cnt, n, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n
			for i := uint32(0); i <= cnt; i++ {
				_, n2, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n2
			}
			out.Write(instrs[start:pos])

		case op == 0x11 || (op >= 0x28 && op <= 0x3e):
			// call_indirect or memory load/store: 2 ULEB128 immediates
			_, n1, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n1
			_, n2, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n2
			out.Write(instrs[start:pos])

		case op == 0x1c:
			// typed select: vec(valtype)
			cnt, n, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n + int(cnt)
			if pos > len(instrs) {
				return nil, false
			}
			out.Write(instrs[start:pos])

		case op == 0x3f || op == 0x40:
			// memory.size / memory.grow: 1 byte (0x00)
			if pos >= len(instrs) {
				return nil, false
			}
			pos++
			out.Write(instrs[start:pos])

		case op == 0x41 || op == 0x42:
			// i32.const / i64.const: 1 SLEB128 immediate
			n, ok := skipSLEB128(instrs[pos:])
			if !ok {
				return nil, false
			}
			pos += n
			out.Write(instrs[start:pos])

		case op == 0x43:
			// f32.const: 4 bytes
			if pos+4 > len(instrs) {
				return nil, false
			}
			pos += 4
			out.Write(instrs[start:pos])

		case op == 0x44:
			// f64.const: 8 bytes
			if pos+8 > len(instrs) {
				return nil, false
			}
			pos += 8
			out.Write(instrs[start:pos])

		case op >= 0x45 && op <= 0xc4:
			// Numeric comparison, arithmetic, conversion, and sign-extension instructions (0 immediates)
			out.WriteByte(op)

		case op == 0xfc:
			subOp, n, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n
			switch {
			case subOp <= 7:
				// trunc_sat_*
			case subOp == 8:
				// memory.init: data_idx (u32) + 0x00
				_, n1, err := readULEB128(instrs[pos:])
				if err != nil || pos+n1+1 > len(instrs) {
					return nil, false
				}
				pos += n1 + 1
			case subOp == 9 || subOp == 13 || (subOp >= 15 && subOp <= 17):
				// data.drop / elem.drop / table.grow / table.size / table.fill: 1 u32
				_, n1, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n1
			case subOp == 10:
				// memory.copy: 0x00 0x00
				if pos+2 > len(instrs) {
					return nil, false
				}
				pos += 2
			case subOp == 11:
				// memory.fill: 0x00
				if pos+1 > len(instrs) {
					return nil, false
				}
				pos++
			case subOp == 12 || subOp == 14:
				// table.init / table.copy: 2 u32
				_, n1, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n1
				_, n2, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n2
			default:
				return nil, false
			}
			out.Write(instrs[start:pos])

		case op == 0xfd:
			subOp, n, err := readULEB128(instrs[pos:])
			if err != nil {
				return nil, false
			}
			pos += n
			switch {
			case subOp <= 11 || subOp == 92 || subOp == 93:
				_, n1, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n1
				_, n2, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n2
			case subOp == 12 || subOp == 13:
				if pos+16 > len(instrs) {
					return nil, false
				}
				pos += 16
			case subOp >= 21 && subOp <= 34:
				if pos+1 > len(instrs) {
					return nil, false
				}
				pos++
			case subOp >= 84 && subOp <= 91:
				_, n1, err := readULEB128(instrs[pos:])
				if err != nil {
					return nil, false
				}
				pos += n1
				_, n2, err := readULEB128(instrs[pos:])
				if err != nil || pos+n2+1 > len(instrs) {
					return nil, false
				}
				pos += n2 + 1
			}
			out.Write(instrs[start:pos])

		default:
			return nil, false
		}
	}
	return out.Bytes(), true
}

func writeSection(buf *bytes.Buffer, id byte, payload []byte) {
	buf.WriteByte(id)
	writeULEB128(buf, uint32(len(payload)))
	buf.Write(payload)
}

func readULEB128(b []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	for i := 0; i < len(b) && i < 5; i++ {
		by := b[i]
		result |= uint32(by&0x7f) << shift
		if by&0x80 == 0 {
			return result, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, fmt.Errorf("invalid ULEB128")
}

func skipSLEB128(b []byte) (int, bool) {
	for i := 0; i < len(b) && i < 10; i++ {
		if b[i]&0x80 == 0 {
			return i + 1, true
		}
	}
	return 0, false
}

func writeULEB128(buf *bytes.Buffer, v uint32) {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		buf.WriteByte(c)
		if v == 0 {
			break
		}
	}
}

func writeSLEB128(buf *bytes.Buffer, v int64) {
	more := true
	for more {
		c := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && (c&0x40) == 0) || (v == -1 && (c&0x40) != 0) {
			more = false
		} else {
			c |= 0x80
		}
		buf.WriteByte(c)
	}
}
