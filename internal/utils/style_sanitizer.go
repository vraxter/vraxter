// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"regexp"
	"strings"
)

var ansibg = regexp.MustCompile(`\x1b\[(4[0-9]|10[0-9]|48;[0-9];[0-9;]+)m`)

func StripANSIBackgrounds(s string) string {
	re := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		content := m[2 : len(m)-1]
		if content == "" {
			// \x1b[m is equivalent to \x1b[0m
			return "\x1b[39;22;23;24;25;27;28;29m"
		}
		parts := strings.Split(content, ";")
		var keep []string
		for i := 0; i < len(parts); i++ {
			p := parts[i]
			if p == "0" || p == "00" {
				keep = append(keep, "39", "22", "23", "24", "25", "27", "28", "29")
				continue
			}
			if p == "48" {
				if i+1 < len(parts) && parts[i+1] == "5" {
					i += 2
				} else if i+1 < len(parts) && parts[i+1] == "2" {
					i += 4
				}
				continue
			}
			// Strip standard (40-47) and high-intensity (100-107) backgrounds, plus default background (49)
			if len(p) == 2 && p[0] == '4' {
				continue
			}
			if len(p) == 3 && p[:2] == "10" {
				continue
			}
			keep = append(keep, p)
		}
		if len(keep) == 0 {
			return ""
		}
		return "\x1b[" + strings.Join(keep, ";") + "m"
	})
}
