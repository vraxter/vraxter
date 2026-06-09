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
	GoBoilerplate       string
	RustBoilerplate     string
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
	GoBoilerplate       string
	RustBoilerplate     string
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
