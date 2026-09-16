// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.vrx
var promptFiles embed.FS

var (
	baseSystemTpl   *template.Template
	specialistTpl   *template.Template
	plannerTpl      *template.Template
	codegenTpl      *template.Template
	GoBoilerplate   string
	RustBoilerplate string
	ZigBoilerplate  string
)

func init() {
	var err error
	baseSystemTpl, err = template.New("base_system.vrx").Funcs(template.FuncMap{
		"prefix": func(p, s string) string {
			if strings.TrimSpace(s) == "" {
				return ""
			}
			lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
			for i, line := range lines {
				lines[i] = p + line
			}
			return strings.Join(lines, "\n")
		},
		"join": strings.Join,
	}).ParseFS(promptFiles, "base_system.vrx")
	if err != nil {
		panic(fmt.Sprintf("Failed to load base_system.vrx template: %v", err))
	}

	specialistTpl, err = template.New("specialist.vrx").Funcs(template.FuncMap{
		"prefix": func(p, s string) string {
			if strings.TrimSpace(s) == "" {
				return ""
			}
			lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
			for i, line := range lines {
				lines[i] = p + line
			}
			return strings.Join(lines, "\n")
		},
		"join": strings.Join,
	}).ParseFS(promptFiles, "specialist.vrx")
	if err != nil {
		panic(fmt.Sprintf("Failed to load specialist.vrx template: %v", err))
	}

	plannerTpl, err = template.New("planner.vrx").Funcs(template.FuncMap{
		"prefix": func(p, s string) string {
			if strings.TrimSpace(s) == "" {
				return ""
			}
			lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
			for i, line := range lines {
				lines[i] = p + line
			}
			return strings.Join(lines, "\n")
		},
	}).ParseFS(promptFiles, "planner.vrx")
	if err != nil {
		panic(fmt.Sprintf("Failed to load planner.vrx template: %v", err))
	}

	codegenTpl, err = template.New("codegen.vrx").Funcs(template.FuncMap{
		"prefix": func(p, s string) string {
			if strings.TrimSpace(s) == "" {
				return ""
			}
			lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
			for i, line := range lines {
				lines[i] = p + line
			}
			return strings.Join(lines, "\n")
		},
	}).ParseFS(promptFiles, "codegen.vrx")
	if err != nil {
		panic(fmt.Sprintf("Failed to load codegen.vrx template: %v", err))
	}

	// Load boilerplates
	goBytes, _ := promptFiles.ReadFile("boilerplate_go.vrx")
	GoBoilerplate = string(goBytes)
	rustBytes, _ := promptFiles.ReadFile("boilerplate_rust.vrx")
	RustBoilerplate = string(rustBytes)

	zigBytes, _ := promptFiles.ReadFile("boilerplate_zig.vrx")
	ZigBoilerplate = string(zigBytes)
}

type BaseSystemParams struct {
	SourceZone          string
	DeviceMap           map[string][]string
	ExistingSpecialists string
	AvailableModels     string
	AvailableTools      string
	MarkerChat          string
	MarkerTool          string
	MarkerCode          string
	GitContext          string
	UserProfile         string
	SemanticMemory      string
	GoBoilerplate       string
	RustBoilerplate     string
	ZigBoilerplate      string
}

type SpecialistParams struct {
	SourceZone          string
	DeviceMap           map[string][]string
	SpecialistName      string
	SpecialistExpertise string
	SpecialistPrompt    string
	AvailableTools      string
	EnforcementSuffix   string
	GitContext          string
	UserProfile         string
	SemanticMemory      string
	GoBoilerplate       string
	RustBoilerplate     string
	ZigBoilerplate      string
	MarkerChat          string
	MarkerTool          string
	MarkerCode          string
}

type PlannerParams struct {
	Query               string
	GitContext          string
	ExistingSpecialists string
}

type CodeGenParams struct {
	Language    string
	Boilerplate string
	Name        string
	Description string
	Spec        string
}

func RenderBaseSystem(params BaseSystemParams) (string, error) {
	if params.GoBoilerplate == "" {
		params.GoBoilerplate = GoBoilerplate
	}
	if params.RustBoilerplate == "" {
		params.RustBoilerplate = RustBoilerplate
	}
	if params.ZigBoilerplate == "" {
		params.ZigBoilerplate = ZigBoilerplate
	}

	var buf bytes.Buffer
	if err := baseSystemTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute base system template: %w", err)
	}
	return buf.String(), nil
}

func RenderSpecialist(params SpecialistParams) (string, error) {
	if params.GoBoilerplate == "" {
		params.GoBoilerplate = GoBoilerplate
	}
	if params.RustBoilerplate == "" {
		params.RustBoilerplate = RustBoilerplate
	}
	if params.ZigBoilerplate == "" {
		params.ZigBoilerplate = ZigBoilerplate
	}

	var buf bytes.Buffer
	if err := specialistTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute specialist template: %w", err)
	}
	return buf.String(), nil
}

func RenderPlanner(params PlannerParams) (string, error) {
	var buf bytes.Buffer
	if err := plannerTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute planner template: %w", err)
	}
	return buf.String(), nil
}

func RenderCodeGen(params CodeGenParams) (string, error) {
	if params.Language == "" {
		params.Language = "go"
	}
	if params.Boilerplate == "" {
		if params.Language == "rust" {
			params.Boilerplate = RustBoilerplate
		} else if params.Language == "zig" {
			params.Boilerplate = ZigBoilerplate
		} else {
			params.Boilerplate = GoBoilerplate
		}
	}

	var buf bytes.Buffer
	if err := codegenTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute codegen template: %w", err)
	}
	return buf.String(), nil
}
