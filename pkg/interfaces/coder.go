// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package interfaces

import (
	"context"
	"github.com/vraxter/vraxter/pkg/types"
)

type SkillCoder interface {
	Compile(name, description, paramsSchema, code string, permissions []string) error
	Language() string
}

type SkillRunner interface {
	Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error)
}
