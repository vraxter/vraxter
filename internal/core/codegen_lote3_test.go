// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package core

import (
	"context"
	"testing"

	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

type stubCodeGenProvider struct {
	reply string
}

func (p *stubCodeGenProvider) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	return interfaces.CompletionResponse{Content: p.reply}, nil
}
func (p *stubCodeGenProvider) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	ch := make(chan interfaces.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *stubCodeGenProvider) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, nil
}
func (p *stubCodeGenProvider) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	return nil, nil
}
func (p *stubCodeGenProvider) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	return nil, nil
}
func (p *stubCodeGenProvider) CheckHealth(ctx context.Context) error { return nil }

type captureManifestCoder struct {
	gotName        string
	gotPermissions []string
	gotManifest    *types.SkillManifestV1
}

func (c *captureManifestCoder) Language() string { return "go" }
func (c *captureManifestCoder) Compile(name, description, paramsSchema, code string, permissions []string) error {
	return c.CompileWithManifest(name, description, paramsSchema, code, permissions, nil)
}
func (c *captureManifestCoder) CompileWithManifest(name, description, paramsSchema, code string, permissions []string, manifestV1 *types.SkillManifestV1) error {
	c.gotName = name
	c.gotPermissions = append([]string(nil), permissions...)
	c.gotManifest = manifestV1
	return nil
}

func TestCodeGenService_PreservesSynthesizedV1ManifestEndToEnd(t *testing.T) {
	router := llm.NewRouterFromEntries([]llm.RouterEntry{
		{
			Config: types.ModelConfig{
				ID:         "m1",
				ProviderID: "prov-local",
				Provider:   "local",
				Model:      "local-coder",
				Priority:   1,
				IsActive:   true,
			},
			Provider: &stubCodeGenProvider{
				reply: `[VRAX_MANIFEST]
{
  "$schema": "https://vraxter.io/schemas/skill-manifest.v1.json",
  "manifest_version": "skill-manifest/1",
  "id": "log-audit",
  "name": "log-audit",
  "version": "1.0.0",
  "description": "Audit local logs",
  "agent": { "risk": "high", "approval": "always" },
  "module": { "file": "log-audit.wasm", "abi": "wasip1" },
  "routing": { "keywords": ["logs", "audit"], "tags": ["sec"] },
  "io": {
    "input": { "via": "stdin", "format": "json", "schema": { "type": "object", "required": ["path"] } },
    "output": { "via": "file", "format": "json", "path": "/output/audit.json" }
  },
  "capabilities": {
    "fs": {
      "mounts": [
        { "guest": "/workspace", "source": "workspace", "subpath": "logs", "mode": "ro" },
        { "guest": "/output", "source": "output", "mode": "rw" }
      ]
    },
    "network": { "mode": "fetch", "allowed_hosts": ["127.0.0.1"] }
  },
  "limits": { "memory_mb": 64, "max_runtime_ms": 15000 }
}
[VRAX_CODE]
package main
func main() {}
`,
			},
		},
	})

	capture := &captureManifestCoder{}
	coderSvc := services.NewCoderService(nil, nil, t.TempDir(), nil)
	coderSvc.RegisterCoder(capture)

	cg := &CodeGenService{Router: router, Coder: coderSvc}
	out := make(chan llm.StreamEvent, 32)
	if err := cg.GenerateAndCompile(context.Background(), out, "log-audit", "Audit local logs", "Inspect logs", "go"); err != nil {
		t.Fatalf("GenerateAndCompile failed: %v", err)
	}

	if capture.gotManifest == nil {
		t.Fatal("expected parsed SkillManifestV1 to be forwarded to ManifestAwareSkillCoder")
	}
	if capture.gotManifest.IO == nil || capture.gotManifest.IO.Output.Via != "file" || capture.gotManifest.IO.Output.Path != "/output/audit.json" {
		t.Fatalf("expected IO output path preserved, got %+v", capture.gotManifest.IO)
	}
	if capture.gotManifest.Capabilities == nil || capture.gotManifest.Capabilities.FS == nil || len(capture.gotManifest.Capabilities.FS.Mounts) != 2 {
		t.Fatalf("expected 2 FS mounts preserved, got %+v", capture.gotManifest.Capabilities)
	}
	if capture.gotManifest.Capabilities.FS.Mounts[0].Source != "workspace" || capture.gotManifest.Capabilities.FS.Mounts[0].Subpath != "logs" {
		t.Fatalf("expected mount[0] source=workspace subpath=logs, got %+v", capture.gotManifest.Capabilities.FS.Mounts[0])
	}
	if capture.gotManifest.Capabilities.Network == nil || len(capture.gotManifest.Capabilities.Network.AllowedHosts) != 1 || capture.gotManifest.Capabilities.Network.AllowedHosts[0] != "127.0.0.1" {
		t.Fatalf("expected network allowed_hosts preserved, got %+v", capture.gotManifest.Capabilities.Network)
	}
	if capture.gotManifest.Limits == nil || capture.gotManifest.Limits.MemoryMB != 64 || capture.gotManifest.Limits.MaxRuntimeMS != 15000 {
		t.Fatalf("expected limits preserved, got %+v", capture.gotManifest.Limits)
	}
}
