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
	"os"
	"sort"
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

type lanEvent struct {
	EventID       string `json:"event_id"`
	TimestampUTC  string `json:"timestamp_utc"`
	EventType     string `json:"event_type"` // "NET_CONNECT", "AUTH_FAILED", "PAYLOAD_TRANSFER"
	SrcIP         string `json:"src_ip"`
	DstIP         string `json:"dst_ip"`
	DstPort       int    `json:"dst_port"`
	Username      string `json:"username,omitempty"`
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
	PayloadMarker string `json:"payload_marker,omitempty"`
}

type triageReport struct {
	Verdict         string   `json:"verdict"`
	EventsAnalyzed  int      `json:"events_analyzed"`
	AttackerIPs     []string `json:"attacker_ips"`
	ScannedPorts    []int    `json:"scanned_ports"`
	BruteForceCount int      `json:"brute_force_failures"`
	PayloadHashes   []string `json:"malicious_payload_sha256"`
	PayloadMarkers  []string `json:"malicious_payload_markers"`
	NFTablesRules   []string `json:"proposed_nftables_rules"`
	YARARule        string   `json:"proposed_yara_rule"`
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
		emitError("vrx-soc-triage", -32700, fmt.Sprintf("stdin read error: %v", err))
		return
	}

	var req skillRequest
	_ = json.Unmarshal(rawIn, &req)
	if req.ID == "" {
		req.ID = "vrx-soc-triage"
	}

	filePath := "/input/lan_incident_events.jsonl"
	if req.Params != nil {
		if fp, ok := req.Params["file"].(string); ok && strings.TrimSpace(fp) != "" {
			filePath = strings.TrimSpace(fp)
		}
	}

	f, err := os.Open(filePath)
	if err != nil {
		emitError(req.ID, -32001, fmt.Sprintf("failed to open SOC incident dataset %s: %v", filePath, err))
		return
	}
	defer f.Close()

	portsBySrc := make(map[string]map[int]bool)
	authFailsBySrc := make(map[string]int)
	payloadHashes := make(map[string]bool)
	payloadMarkers := make(map[string]bool)
	attackerSet := make(map[string]bool)
	totalEvents := 0

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev lanEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		totalEvents++

		switch ev.EventType {
		case "NET_CONNECT":
			if portsBySrc[ev.SrcIP] == nil {
				portsBySrc[ev.SrcIP] = make(map[int]bool)
			}
			portsBySrc[ev.SrcIP][ev.DstPort] = true
		case "AUTH_FAILED":
			authFailsBySrc[ev.SrcIP]++
		case "PAYLOAD_TRANSFER":
			if ev.PayloadMarker != "" && strings.Contains(strings.ToUpper(ev.PayloadMarker), "BEACON") {
				attackerSet[ev.SrcIP] = true
				payloadMarkers[ev.PayloadMarker] = true
				if ev.PayloadSHA256 != "" {
					payloadHashes[ev.PayloadSHA256] = true
				}
			}
		}
	}

	scannedPortSet := make(map[int]bool)
	bruteForceTotal := 0
	for src, ports := range portsBySrc {
		if len(ports) >= 4 {
			attackerSet[src] = true
			for p := range ports {
				scannedPortSet[p] = true
			}
		}
	}
	for src, fails := range authFailsBySrc {
		if fails >= 4 {
			attackerSet[src] = true
			bruteForceTotal += fails
		}
	}

	var attackers []string
	for ip := range attackerSet {
		attackers = append(attackers, ip)
	}
	sort.Strings(attackers)

	var ports []int
	for p := range scannedPortSet {
		ports = append(ports, p)
	}
	sort.Ints(ports)

	var hashes []string
	for h := range payloadHashes {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	var markers []string
	for m := range payloadMarkers {
		markers = append(markers, m)
	}
	sort.Strings(markers)

	var nftRules []string
	for _, ip := range attackers {
		nftRules = append(nftRules, fmt.Sprintf(`nft add rule inet vraxter_soc input ip saddr %s counter drop comment "vraxter-soc-containment"`, ip))
	}

	var yaraBuilder strings.Builder
	if len(markers) > 0 || len(hashes) > 0 {
		yaraBuilder.WriteString("rule Vraxter_SOC_IsolatedLAN_Beacon_Stage2 {\n")
		yaraBuilder.WriteString("  meta:\n")
		yaraBuilder.WriteString("    author = \"Vraxter Sovereign SOC Triage\"\n")
		yaraBuilder.WriteString("    severity = \"critical\"\n")
		if len(hashes) > 0 {
			yaraBuilder.WriteString(fmt.Sprintf("    sha256 = \"%s\"\n", hashes[0]))
		}
		yaraBuilder.WriteString("  strings:\n")
		for idx, m := range markers {
			yaraBuilder.WriteString(fmt.Sprintf("    $m%d = \"%s\" ascii wide\n", idx, m))
		}
		yaraBuilder.WriteString("  condition:\n")
		yaraBuilder.WriteString("    any of them\n")
		yaraBuilder.WriteString("}")
	}

	verdict := "NO_THREATS_DETECTED"
	if len(attackers) > 0 {
		verdict = "LATERAL_INTRUSION_AND_PAYLOAD_CONTAINMENT_PROPOSED"
	}

	report := triageReport{
		Verdict:         verdict,
		EventsAnalyzed:  totalEvents,
		AttackerIPs:     attackers,
		ScannedPorts:    ports,
		BruteForceCount: bruteForceTotal,
		PayloadHashes:   hashes,
		PayloadMarkers:  markers,
		NFTablesRules:   nftRules,
		YARARule:        yaraBuilder.String(),
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
