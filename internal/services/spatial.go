// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package services

import (
	"encoding/json"
	"os"
)

// SpatialService provides unified spatial awareness for Vraxter, mapping physical Zones
// to specific devices/speakers via an internal JSON config or Google Local Home SDK.
type SpatialService struct {
	enableGoogleHome bool
	configPath       string
}

// NewSpatialService initializes the unified spatial provider.
func NewSpatialService(enableGoogleHome bool, configPath string) *SpatialService {
	return &SpatialService{
		enableGoogleHome: enableGoogleHome,
		configPath:       configPath,
	}
}

// GetDeviceMap returns a map of Zones -> Device/Speaker names.
func (s *SpatialService) GetDeviceMap() map[string][]string {
	deviceMap := make(map[string][]string)

	// 1. Load from internal local config fallback (if exists)
	data, err := os.ReadFile(s.configPath)
	if err == nil {
		var localMap map[string][]string
		if err := json.Unmarshal(data, &localMap); err == nil {
			for k, v := range localMap {
				deviceMap[k] = v
			}
		}
	}

	// 2. Merge/Override from Google Home APIs (if active)
	if s.enableGoogleHome {
		// Mock implementation: In reality, this would query Google Home API.
		deviceMap["living_room"] = append(deviceMap["living_room"], "Living Room TV", "Nest Audio")
		deviceMap["office"] = append(deviceMap["office"], "Office Speaker")
		deviceMap["kitchen"] = append(deviceMap["kitchen"], "Kitchen Display")
		deviceMap["bedroom"] = append(deviceMap["bedroom"], "Bedroom Clock")
	}

	return deviceMap
}
