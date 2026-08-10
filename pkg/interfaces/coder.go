package interfaces

import (
	"context"
	"github.com/patagonicrune/vraxter/pkg/types"
)

type SkillCoder interface {
	Compile(name, description, paramsSchema, code string, permissions []string) error
	Language() string
}

type SkillRunner interface {
	Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error)
}
