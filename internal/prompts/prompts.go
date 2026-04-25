package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed *.md *.vrx
var promptFiles embed.FS

var (
	baseSystemTpl *template.Template
	specialistTpl *template.Template
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
	}).ParseFS(promptFiles, "specialist.vrx")
	if err != nil {
		panic(fmt.Sprintf("Failed to load specialist.vrx template: %v", err))
	}
}

type BaseSystemParams struct {
	ExistingSpecialists string
	AvailableModels     string
	AvailableTools      string
	MarkerChat          string
	MarkerTool          string
	MarkerCode          string
	GitContext          string
	UserProfile         string
	SemanticMemory      string
}

type SpecialistParams struct {
	SpecialistName      string
	SpecialistExpertise string
	SpecialistPrompt    string
	AvailableTools      string
	EnforcementSuffix   string
	GitContext          string
	UserProfile         string
	SemanticMemory      string
}

func RenderBaseSystem(params BaseSystemParams) (string, error) {
	var buf bytes.Buffer
	if err := baseSystemTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute base system template: %w", err)
	}
	return buf.String(), nil
}

func RenderSpecialist(params SpecialistParams) (string, error) {
	var buf bytes.Buffer
	if err := specialistTpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("failed to execute specialist template: %w", err)
	}
	return buf.String(), nil
}
