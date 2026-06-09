package services

import (
	"context"
	"fmt"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/utils"
	"github.com/patagonicrune/vraxter/pkg/types"
)

type ModelManager struct {
	modelRepo    *db.ModelRepository
	providerRepo *db.ProviderRepository
}

func NewModelManager(modelRepo *db.ModelRepository, providerRepo *db.ProviderRepository) *ModelManager {
	return &ModelManager{
		modelRepo:    modelRepo,
		providerRepo: providerRepo,
	}
}

func (m *ModelManager) AddModel(ctx context.Context, providerIDOrName, remoteModelID string, priority int, alias string) (string, error) {
	// 1. Resolve Provider
	p, err := m.providerRepo.GetByIDOrName(providerIDOrName)
	if err != nil {
		return "", fmt.Errorf("could not find provider: %w", err)
	}

	// 2. Generate Model Metadata
	id := utils.GenerateShortID(remoteModelID)
	if alias == "" {
		alias = fmt.Sprintf("%s-%s", p.Name, remoteModelID)
	}

	cfg := types.ModelConfig{
		ID:         id,
		ProviderID: p.ID,
		Alias:      alias,
		Model:      remoteModelID,
		Priority:   priority,
		IsActive:   true,
	}

	if err := m.modelRepo.UpsertModel(cfg); err != nil {
		return "", fmt.Errorf("failed to save model: %w", err)
	}

	return id, nil
}

func (m *ModelManager) ListModels() ([]types.ModelConfig, error) {
	return m.modelRepo.GetAllModels()
}

func (m *ModelManager) GetModel(idOrAlias string) (*types.ModelConfig, error) {
	return m.modelRepo.GetModelByID(idOrAlias)
}

func (m *ModelManager) UpdateModel(idOrAlias string, priority *int, isActive *bool, alias *string, modelName *string, useCasePriorities map[string]int) error {
	cfg, err := m.modelRepo.GetModelByID(idOrAlias)
	if err != nil {
		return err
	}

	if priority != nil {
		cfg.Priority = *priority
	}
	if isActive != nil {
		cfg.IsActive = *isActive
	}
	if alias != nil {
		cfg.Alias = *alias
	}
	if modelName != nil {
		cfg.Model = *modelName
	}
	if useCasePriorities != nil {
		cfg.UseCasePriorities = useCasePriorities
	}

	return m.modelRepo.UpsertModel(*cfg)
}

func (m *ModelManager) DeleteModel(id string) error {
	return m.modelRepo.DeleteModel(id)
}
