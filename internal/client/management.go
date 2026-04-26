package client

import (
	"context"
	v1 "github.com/patagonicrune/vraxter/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ManagementClient provides a high-level wrapper over the gRPC Management RPCs
type ManagementClient struct {
	conn *grpc.ClientConn
	api  v1.AgentServiceClient
}

func NewManagementClient(addr string) (*ManagementClient, error) {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &ManagementClient{
		conn: conn,
		api:  v1.NewAgentServiceClient(conn),
	}, nil
}

func (c *ManagementClient) Close() error {
	return c.conn.Close()
}

func (c *ManagementClient) ListProviders(ctx context.Context) ([]*v1.ProviderInfo, error) {
	resp, err := c.api.ListProviders(ctx, &v1.ListProvidersRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Providers, nil
}

func (c *ManagementClient) ConfigureProvider(ctx context.Context, name, pType, apiKey, baseURL string) (string, error) {
	resp, err := c.api.ConfigureProvider(ctx, &v1.ConfigureProviderRequest{
		Name:    name,
		Type:    pType,
		ApiKey:  apiKey,
		BaseUrl: baseURL,
	})
	if err != nil {
		return "", err
	}
	return resp.ProviderId, nil
}

func (c *ManagementClient) DiscoverModels(ctx context.Context, providerID string) ([]string, error) {
	resp, err := c.api.DiscoverModels(ctx, &v1.DiscoverModelsRequest{ProviderId: providerID})
	if err != nil {
		return nil, err
	}
	return resp.Models, nil
}

func (c *ManagementClient) ListModels(ctx context.Context, providerID string) ([]*v1.ModelInfo, error) {
	resp, err := c.api.ListModels(ctx, &v1.ListModelsRequest{ProviderId: providerID})
	if err != nil {
		return nil, err
	}
	return resp.Models, nil
}

func (c *ManagementClient) RegisterModel(ctx context.Context, providerID, modelName, alias string, priority int) (string, error) {
	resp, err := c.api.RegisterModel(ctx, &v1.RegisterModelRequest{
		ProviderId: providerID,
		ModelName:  modelName,
		Alias:      alias,
		Priority:   int32(priority),
	})
	if err != nil {
		return "", err
	}
	return resp.ModelId, nil
}

func (c *ManagementClient) GetSupportedProviders(ctx context.Context) ([]string, error) {
	resp, err := c.api.GetSupportedProviders(ctx, &v1.GetSupportedProvidersRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Providers, nil
}
