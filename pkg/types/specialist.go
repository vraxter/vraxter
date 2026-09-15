// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package types

import "time"

type Specialist struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Expertise    string    `json:"expertise"`
	ModelID      string    `json:"model_id,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
