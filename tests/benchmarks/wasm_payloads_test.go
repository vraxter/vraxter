// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package benchmarks

import (
	"bytes"
	"math"
	"sort"
	"time"
)

// LatencyStats summarizes latency distribution across benchmark iterations.
type LatencyStats struct {
	Samples int     `json:"samples"`
	MinUS   float64 `json:"min_us"`
	P50US   float64 `json:"p50_us"`
	P95US   float64 `json:"p95_us"`
	P99US   float64 `json:"p99_us"`
	MaxUS   float64 `json:"max_us"`
	MeanUS  float64 `json:"mean_us"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
}

func computeLatencyStats(durations []time.Duration) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}
	us := make([]float64, len(durations))
	var sum float64
	for i, d := range durations {
		v := float64(d.Nanoseconds()) / 1000.0
		us[i] = v
		sum += v
	}
	sort.Float64s(us)

	percentile := func(p float64) float64 {
		if len(us) == 1 {
			return us[0]
		}
		idx := int(math.Ceil((p/100.0)*float64(len(us)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(us) {
			idx = len(us) - 1
		}
		return round2(us[idx])
	}

	p50 := percentile(50)
	p95 := percentile(95)
	p99 := percentile(99)

	return LatencyStats{
		Samples: len(us),
		MinUS:   round2(us[0]),
		P50US:   p50,
		P95US:   p95,
		P99US:   p99,
		MaxUS:   round2(us[len(us)-1]),
		MeanUS:  round2(sum / float64(len(us))),
		P50MS:   round3(p50 / 1000.0),
		P95MS:   round3(p95 / 1000.0),
		P99MS:   round3(p99 / 1000.0),
	}
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func appendULEB(buf *bytes.Buffer, v uint32) {
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

func appendSLEB(buf *bytes.Buffer, v int64) {
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

func writeWASMSection(out *bytes.Buffer, id byte, payload []byte) {
	out.WriteByte(id)
	appendULEB(out, uint32(len(payload)))
	out.Write(payload)
}

// buildTelemetryAnalyzerWASM constructs a deterministic WASI .wasm binary that writes
// a fixed JSONRPC telemetry analysis result to stdout via wasi_snapshot_preview1.fd_write.
func buildTelemetryAnalyzerWASM(jsonOutput string) []byte {
	if jsonOutput == "" {
		jsonOutput = `{"jsonrpc":"2.0","result":"{\"anomaly_score\":0.9421,\"frames\":128,\"verdict\":\"ROTOR_VIBRATION_ALERT\"}","id":"telemetry"}`
	}
	msgBytes := []byte(jsonOutput)
	msgLen := uint32(len(msgBytes))

	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	// Type section: type 0 = (i32,i32,i32,i32)->i32, type 1 = ()->()
	writeWASMSection(&out, 1, []byte{
		0x02,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x00,
	})

	// Import section: wasi_snapshot_preview1.fd_write (func 0, type 0)
	var imp bytes.Buffer
	imp.WriteByte(0x01)
	imp.WriteByte(byte(len("wasi_snapshot_preview1")))
	imp.WriteString("wasi_snapshot_preview1")
	imp.WriteByte(byte(len("fd_write")))
	imp.WriteString("fd_write")
	imp.Write([]byte{0x00, 0x00})
	writeWASMSection(&out, 2, imp.Bytes())

	// Function section: func 1 uses type 1
	writeWASMSection(&out, 3, []byte{0x01, 0x01})

	// Memory section: 1 memory, min 1 page
	writeWASMSection(&out, 5, []byte{0x01, 0x00, 0x01})

	// Export section: memory (mem 0), _start (func 1)
	writeWASMSection(&out, 7, []byte{
		0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x01,
	})

	// Code section: _start runs a small deterministic arithmetic loop (16 iterations)
	// and writes the JSON payload at offset 32 (iov at offset 0) to stdout (fd 1).
	var body bytes.Buffer
	body.Write([]byte{
		0x01, 0x01, 0x7f, // 1 local of type i32 (local 0 = counter)
		0x41, 0x10, // i32.const 16
		0x21, 0x00, // local.set 0
		0x03, 0x40, // loop (empty)
		0x20, 0x00, // local.get 0
		0x41, 0x01, // i32.const 1
		0x6b,       // i32.sub
		0x22, 0x00, // local.tee 0
		0x0d, 0x00, // br_if 0
		0x0b,       // end loop
		// fd_write(1, 0, 1, 16)
		0x41, 0x01,
		0x41, 0x00,
		0x41, 0x01,
		0x41, 0x10,
		0x10, 0x00,
		0x1a,
		0x0b,
	})
	var codeSec bytes.Buffer
	codeSec.WriteByte(0x01)
	appendULEB(&codeSec, uint32(body.Len()))
	codeSec.Write(body.Bytes())
	writeWASMSection(&out, 10, codeSec.Bytes())

	// Data section: segment 0 at offset 0 (iov = {ptr: 32, len: msgLen}), segment 1 at offset 32 (msgBytes)
	var dataSec bytes.Buffer
	dataSec.WriteByte(0x02)
	// Segment 0 at offset 0: 8 bytes
	dataSec.Write([]byte{0x00, 0x41, 0x00, 0x0b, 0x08})
	dataSec.Write([]byte{
		32, 0, 0, 0,
		byte(msgLen), byte(msgLen >> 8), byte(msgLen >> 16), byte(msgLen >> 24),
	})
	// Segment 1 at offset 32: msgBytes
	dataSec.Write([]byte{0x00, 0x41, 0x20, 0x0b})
	appendULEB(&dataSec, msgLen)
	dataSec.Write(msgBytes)
	writeWASMSection(&out, 11, dataSec.Bytes())

	return out.Bytes()
}

// buildHostileHTTPGetWASM constructs a WASI .wasm binary that imports vrax_sandbox.http_get
// and attempts an outbound HTTP request to targetURL. If http_get returns < 0 (blocked by shield),
// it traps with unreachable.
func buildHostileHTTPGetWASM(targetURL string) []byte {
	urlBytes := []byte(targetURL)
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	// Type section: type 0 = (i32,i32,i32,i32)->i32, type 1 = ()->()
	writeWASMSection(&out, 1, []byte{
		0x02,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x00,
	})

	// Import section: vrax_sandbox.http_get (func 0, type 0)
	var imp bytes.Buffer
	imp.WriteByte(0x01)
	imp.WriteByte(byte(len("vrax_sandbox")))
	imp.WriteString("vrax_sandbox")
	imp.WriteByte(byte(len("http_get")))
	imp.WriteString("http_get")
	imp.Write([]byte{0x00, 0x00})
	writeWASMSection(&out, 2, imp.Bytes())

	// Function section: func 1 uses type 1
	writeWASMSection(&out, 3, []byte{0x01, 0x01})

	// Memory section: 1 memory, min 1 page
	writeWASMSection(&out, 5, []byte{0x01, 0x00, 0x01})

	// Export section: memory (mem 0), _start (func 1)
	writeWASMSection(&out, 7, []byte{
		0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x01,
	})

	// Code section: call http_get(32, len(url), 256, 512); if < 0 -> unreachable
	var body bytes.Buffer
	body.WriteByte(0x00) // 0 locals
	body.WriteByte(0x41)
	appendSLEB(&body, 32)
	body.WriteByte(0x41)
	appendSLEB(&body, int64(len(urlBytes)))
	body.WriteByte(0x41)
	appendSLEB(&body, 256)
	body.WriteByte(0x41)
	appendSLEB(&body, 512)
	body.Write([]byte{
		0x10, 0x00, // call http_get
		0x41, 0x00, // i32.const 0
		0x48,       // i32.lt_s
		0x04, 0x40, // if
		0x00, // unreachable
		0x0b, // end if
		0x0b, // end func
	})
	var codeSec bytes.Buffer
	codeSec.WriteByte(0x01)
	appendULEB(&codeSec, uint32(body.Len()))
	codeSec.Write(body.Bytes())
	writeWASMSection(&out, 10, codeSec.Bytes())

	// Data section: urlBytes at offset 32
	var dataSec bytes.Buffer
	dataSec.WriteByte(0x01)
	dataSec.Write([]byte{0x00, 0x41, 0x20, 0x0b})
	appendULEB(&dataSec, uint32(len(urlBytes)))
	dataSec.Write(urlBytes)
	writeWASMSection(&out, 11, dataSec.Bytes())

	return out.Bytes()
}

// buildHostileInfiniteLoopWASM constructs a WASI .wasm binary with an infinite CPU loop (`loop ... br 0`).
func buildHostileInfiniteLoopWASM() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		0x0a, 0x09, 0x01,
		0x07, 0x00,
		0x03, 0x40, // loop
		0x0c, 0x00, // br 0
		0x0b, // end loop
		0x0b, // end func
	}
}

// buildHostileMemoryBombWASM constructs a WASI .wasm binary that attempts to allocate
// pagesToGrow additional 64KB memory pages via memory.grow and traps if denied (-1).
func buildHostileMemoryBombWASM(pagesToGrow int64) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	writeWASMSection(&out, 1, []byte{0x01, 0x60, 0x00, 0x00})
	writeWASMSection(&out, 3, []byte{0x01, 0x00})
	writeWASMSection(&out, 5, []byte{0x01, 0x00, 0x01})
	writeWASMSection(&out, 7, []byte{
		0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
	})

	var body bytes.Buffer
	body.WriteByte(0x00) // 0 locals
	body.WriteByte(0x41) // i32.const pagesToGrow
	appendSLEB(&body, pagesToGrow)
	body.Write([]byte{
		0x40, 0x00, // memory.grow 0
		0x41, 0x00, // i32.const 0
		0x48,       // i32.lt_s (true when memory.grow returns -1)
		0x04, 0x40, // if
		0x00, // unreachable
		0x0b, // end if
		0x0b, // end func
	})
	var codeSec bytes.Buffer
	codeSec.WriteByte(0x01)
	appendULEB(&codeSec, uint32(body.Len()))
	codeSec.Write(body.Bytes())
	writeWASMSection(&out, 10, codeSec.Bytes())
	return out.Bytes()
}

// buildHostileDiskWriteWASM constructs a WASI .wasm binary that attempts to create relFilename
// on preopened mount fd=3 via wasi_snapshot_preview1.path_open (O_CREAT) and traps if denied.
func buildHostileDiskWriteWASM(relFilename string) []byte {
	pathBytes := []byte(relFilename)
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	// Type 0: path_open (i32,i32,i32,i32,i32,i64,i64,i32,i32)->i32
	// Type 1: ()->()
	writeWASMSection(&out, 1, []byte{
		0x02,
		0x60, 0x09, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7e, 0x7e, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x00,
	})

	var imp bytes.Buffer
	imp.WriteByte(0x01)
	imp.WriteByte(byte(len("wasi_snapshot_preview1")))
	imp.WriteString("wasi_snapshot_preview1")
	imp.WriteByte(byte(len("path_open")))
	imp.WriteString("path_open")
	imp.Write([]byte{0x00, 0x00})
	writeWASMSection(&out, 2, imp.Bytes())

	writeWASMSection(&out, 3, []byte{0x01, 0x01})
	writeWASMSection(&out, 5, []byte{0x01, 0x00, 0x01})
	writeWASMSection(&out, 7, []byte{
		0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x01,
	})

	var body bytes.Buffer
	body.WriteByte(0x00) // 0 locals
	body.Write([]byte{
		0x41, 0x03, // dirfd = 3 (first preopened mount)
		0x41, 0x00, // dirflags = 0
		0x41, 0x20, // path_ptr = 32
	})
	body.WriteByte(0x41)
	appendSLEB(&body, int64(len(pathBytes)))
	body.Write([]byte{
		0x41, 0x01, // oflags = 1 (O_CREAT)
		0x42, 0xff, 0xff, 0x03, // fs_rights_base
		0x42, 0xff, 0xff, 0x03, // fs_rights_inheriting
		0x41, 0x00, // fdflags = 0
		0x41, 0x10, // opened_fd_ptr = 16
		0x10, 0x00, // call path_open
		0x45,       // i32.eqz (0 == success)
		0x45,       // i32.eqz (1 == error)
		0x04, 0x40, // if error != 0
		0x00, // unreachable
		0x0b, // end if
		0x0b, // end func
	})
	var codeSec bytes.Buffer
	codeSec.WriteByte(0x01)
	appendULEB(&codeSec, uint32(body.Len()))
	codeSec.Write(body.Bytes())
	writeWASMSection(&out, 10, codeSec.Bytes())

	var dataSec bytes.Buffer
	dataSec.WriteByte(0x01)
	dataSec.Write([]byte{0x00, 0x41, 0x20, 0x0b})
	appendULEB(&dataSec, uint32(len(pathBytes)))
	dataSec.Write(pathBytes)
	writeWASMSection(&out, 11, dataSec.Bytes())

	return out.Bytes()
}
