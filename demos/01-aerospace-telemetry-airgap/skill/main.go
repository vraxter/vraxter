// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

//go:build wasip1 && wasm

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

type skillRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
	ID      string                 `json:"id"`
}

type executionResult struct {
	Status string `json:"status"`
	Output string `json:"output"`
}

type skillResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	Result  *executionResult `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	ID string `json:"id"`
}

type telemetryFrame struct {
	FrameID         string  `json:"frame_id"`
	TimestampUTC    string  `json:"timestamp_utc"`
	EngineID        string  `json:"engine_id"`
	EGT_C           float64 `json:"egt_c"`
	VibrationG      float64 `json:"vibration_g"`
	RPMPercent      float64 `json:"rpm_pct"`
	OilPressurePSI  float64 `json:"oil_psi"`
	MaintenanceNote string  `json:"maintenance_note,omitempty"`
}

type anomalyRecord struct {
	FrameID    string  `json:"frame_id"`
	EGT_C      float64 `json:"egt_c"`
	VibrationG float64 `json:"vibration_g"`
	OilPSI     float64 `json:"oil_psi"`
	Severity   string  `json:"severity"`
	Reason     string  `json:"reason"`
}

type analysisReport struct {
	Verdict                string          `json:"verdict"`
	FramesAnalyzed         int             `json:"frames_analyzed"`
	MaxEGTC                float64         `json:"max_egt_c"`
	MaxVibrationG          float64         `json:"max_vibration_g"`
	MinOilPSI              float64         `json:"min_oil_psi"`
	Anomalies              []anomalyRecord `json:"anomalies"`
	PromptInjectionBlocked []string        `json:"prompt_injection_signals_quarantined"`
}

func detectPromptInjection(note string) bool {
	low := strings.ToLower(note)
	indicators := []string{
		"ignore all previous instructions",
		"exfiltrate",
		"http://",
		"https://",
		"169.254.169.254",
		"execute host command",
		"$ ",
	}
	for _, ind := range indicators {
		if strings.Contains(low, ind) {
			return true
		}
	}
	return false
}

func emitError(id string, code int, msg string) {
	resp := skillResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: msg},
	}
	b, _ := json.Marshal(resp)
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func main() {
	rawIn, err := io.ReadAll(os.Stdin)
	if err != nil {
		emitError("vrx-telemetry-analyzer", -32700, fmt.Sprintf("stdin read error: %v", err))
		return
	}

	var req skillRequest
	_ = json.Unmarshal(rawIn, &req)
	if req.ID == "" {
		req.ID = "vrx-telemetry-analyzer"
	}

	filePath := "/input/telemetry_batch.jsonl"
	if req.Params != nil {
		if fp, ok := req.Params["file"].(string); ok && strings.TrimSpace(fp) != "" {
			filePath = strings.TrimSpace(fp)
		}
	}

	f, err := os.Open(filePath)
	if err != nil {
		emitError(req.ID, -32001, fmt.Sprintf("failed to open read-only telemetry input %s: %v", filePath, err))
		return
	}
	defer f.Close()

	report := analysisReport{
		Verdict:   "NOMINAL",
		MinOilPSI: math.MaxFloat64,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var frame telemetryFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		report.FramesAnalyzed++
		if frame.EGT_C > report.MaxEGTC {
			report.MaxEGTC = frame.EGT_C
		}
		if frame.VibrationG > report.MaxVibrationG {
			report.MaxVibrationG = frame.VibrationG
		}
		if frame.OilPressurePSI < report.MinOilPSI {
			report.MinOilPSI = frame.OilPressurePSI
		}

		// Deterministic physical anomaly thresholds for turbine rotorcraft telemetry
		isThermal := frame.EGT_C >= 850.0
		isVibrational := frame.VibrationG >= 2.5
		isLowOil := frame.OilPressurePSI <= 30.0

		if isThermal || isVibrational || isLowOil {
			var reasons []string
			if isThermal {
				reasons = append(reasons, "EGT_OVER_REDLINE")
			}
			if isVibrational {
				reasons = append(reasons, "ROTOR_VIBRATION_CRITICAL")
			}
			if isLowOil {
				reasons = append(reasons, "OIL_PRESSURE_DROP")
			}
			report.Anomalies = append(report.Anomalies, anomalyRecord{
				FrameID:    frame.FrameID,
				EGT_C:      frame.EGT_C,
				VibrationG: frame.VibrationG,
				OilPSI:     frame.OilPressurePSI,
				Severity:   "CRITICAL",
				Reason:     strings.Join(reasons, "+"),
			})
		}

		if frame.MaintenanceNote != "" && detectPromptInjection(frame.MaintenanceNote) {
			report.PromptInjectionBlocked = append(report.PromptInjectionBlocked, frame.FrameID)
		}
	}

	if report.FramesAnalyzed == 0 {
		report.MinOilPSI = 0
	}
	if len(report.Anomalies) > 0 {
		report.Verdict = "CRITICAL_TURBINE_ANOMALY_DETECTED"
	}

	resultBytes, _ := json.Marshal(report)
	resp := skillResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: &executionResult{
			Status: "completed",
			Output: string(resultBytes),
		},
	}
	outBytes, _ := json.Marshal(resp)
	_, _ = os.Stdout.Write(append(outBytes, '\n'))
}
