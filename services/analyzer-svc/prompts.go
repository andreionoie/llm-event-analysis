package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
)

const promptEventsLimit = 50

//go:embed prompts/*.md
var promptsFS embed.FS

type PromptTemplate struct {
	Config   *PromptConfig
	Template *template.Template
}

type PromptConfig struct {
	Version     string `yaml:"version"`
	Description string `yaml:"description"`

	Model           string   `yaml:"model"`
	Temperature     *float32 `yaml:"temperature"`
	MaxOutputTokens *int     `yaml:"max_output_tokens"`
	StopSequences   []string `yaml:"stop_sequences"`

	InputVariables []PromptInput `yaml:"input_variables"`
}

func (c *PromptConfig) ApplyTo(config *genai.GenerateContentConfig) {
	if c == nil || config == nil {
		return
	}
	if c.Temperature != nil {
		config.Temperature = c.Temperature
	}
	if c.MaxOutputTokens != nil {
		config.MaxOutputTokens = int32(*c.MaxOutputTokens)
	}
	if len(c.StopSequences) > 0 {
		config.StopSequences = c.StopSequences
	}
}

type PromptInput struct {
	Name string `yaml:"name"`
	Desc string `yaml:"desc"`
}

type PromptLibrary struct {
	Analyze       *PromptTemplate
	Tier1Triaging *PromptTemplate
	Tier2Triaging *PromptTemplate
}

type PromptData struct {
	Events        []common.Event
	Question      string
	OverflowCount int
}

type PromptPair struct {
	System string
	User   string
	Config *PromptConfig
}

func NewPromptLibrary(fsys fs.FS) (*PromptLibrary, error) {
	analyze, err := loadPromptTemplate(fsys, "prompts/analyze.md")
	if err != nil {
		return nil, err
	}

	tier1, err := loadPromptTemplate(fsys, "prompts/triage_tier1.md")
	if err != nil {
		return nil, err
	}

	tier2, err := loadPromptTemplate(fsys, "prompts/triage_tier2.md")
	if err != nil {
		return nil, err
	}

	return &PromptLibrary{
		Analyze:       analyze,
		Tier1Triaging: tier1,
		Tier2Triaging: tier2,
	}, nil
}

func (p *PromptLibrary) RenderAnalyzePrompt(question string, eventList []common.Event) (*PromptPair, error) {
	if p == nil || p.Analyze == nil {
		return nil, fmt.Errorf("prompt library is not initialized")
	}

	promptEvents, overflow := selectPromptEvents(eventList, promptEventsLimit)
	data := PromptData{
		Events:        promptEvents,
		Question:      question,
		OverflowCount: overflow,
	}
	return renderPromptPair(p.Analyze, data)
}

func (p *PromptLibrary) RenderTier1TriagingPrompt(summaries []common.EventSummary) (*PromptPair, error) {
	if p == nil || p.Tier1Triaging == nil {
		return nil, fmt.Errorf("tier1 prompt not loaded")
	}
	data := struct{ Summaries []common.EventSummary }{Summaries: summaries}
	return renderPromptPairAny(p.Tier1Triaging, data)
}

func (p *PromptLibrary) RenderTier2TriagingPrompt(events []common.Event) (*PromptPair, error) {
	if p == nil || p.Tier2Triaging == nil {
		return nil, fmt.Errorf("tier2 prompt not loaded")
	}
	data := struct{ Events []common.Event }{Events: events}
	return renderPromptPairAny(p.Tier2Triaging, data)
}

func renderPromptPair(prompt *PromptTemplate, data PromptData) (*PromptPair, error) {
	return renderPromptPairAny(prompt, data)
}

func renderPromptPairAny(prompt *PromptTemplate, data any) (*PromptPair, error) {
	var systemBuf, userBuf bytes.Buffer

	if prompt.Template.Lookup("system") != nil {
		if err := prompt.Template.ExecuteTemplate(&systemBuf, "system", data); err != nil {
			return nil, fmt.Errorf("render system prompt: %w", err)
		}
	}

	if err := prompt.Template.ExecuteTemplate(&userBuf, "user", data); err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}

	return &PromptPair{
		System: strings.TrimSpace(systemBuf.String()),
		User:   strings.TrimSpace(userBuf.String()),
		Config: prompt.Config,
	}, nil
}

func selectPromptEvents(eventList []common.Event, limit int) ([]common.Event, int) {
	overflow := 0
	if len(eventList) > limit {
		overflow = len(eventList) - limit
		eventList = eventList[:limit]
	}

	return eventList, overflow
}

func truncatePayload(payload map[string]any, maxLen int) string {
	if len(payload) == 0 {
		return "{}"
	}
	data, _ := json.Marshal(payload)
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

func loadPromptTemplate(fsys fs.FS, path string) (*PromptTemplate, error) {
	raw, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	frontmatter, body, hasFrontmatter, err := splitFrontmatter(string(raw))
	if err != nil {
		return nil, err
	}
	if !hasFrontmatter {
		return nil, fmt.Errorf("prompt config missing frontmatter")
	}

	tmpl, err := template.New(path).Funcs(promptFuncMap()).Parse(body)
	if err != nil {
		return nil, err
	}

	parsed, err := parsePromptConfig(frontmatter)
	if err != nil {
		return nil, err
	}

	slog.Info("loaded prompt", "path", path, "version", parsed.Version, "description", parsed.Description)
	return &PromptTemplate{
		Config:   parsed,
		Template: tmpl,
	}, nil
}

func splitFrontmatter(input string) (string, string, bool, error) {
	const delimiter = "---\n"
	normalizedNewlines := strings.ReplaceAll(input, "\r\n", "\n")
	if !strings.HasPrefix(normalizedNewlines, delimiter) {
		return "", input, false, nil
	}

	parts := strings.SplitN(normalizedNewlines, delimiter, 3)
	if len(parts) < 3 {
		return "", input, false, fmt.Errorf("malformed frontmatter: closing delimiter not found")
	}

	return strings.TrimRight(parts[1], "\n"), strings.TrimLeft(parts[2], "\n"), true, nil
}

func parsePromptConfig(frontmatter string) (*PromptConfig, error) {
	if strings.TrimSpace(frontmatter) == "" {
		return nil, fmt.Errorf("prompt config missing model")
	}

	var config PromptConfig
	if err := yaml.Unmarshal([]byte(frontmatter), &config); err != nil {
		return nil, fmt.Errorf("parse prompt config: %w", err)
	}

	config.Model = strings.TrimSpace(config.Model)
	if config.Model == "" {
		return nil, fmt.Errorf("prompt config missing model")
	}

	return &config, nil
}

func promptFuncMap() template.FuncMap {
	return template.FuncMap{
		"truncate": func(payload any, maxLen int) string {
			switch v := payload.(type) {
			case nil:
				return "{}"
			case map[string]any:
				return truncatePayload(v, maxLen)
			case string:
				return truncateString(v, maxLen)
			case []byte:
				return truncateString(string(v), maxLen)
			default:
				data, err := json.Marshal(v)
				if err != nil {
					return "{}"
				}
				return truncateString(string(data), maxLen)
			}
		},
		"timeFmt": func(t time.Time) string {
			return t.Format(time.RFC3339)
		},
	}
}

func truncateString(value string, maxLen int) string {
	if value == "" {
		return "{}"
	}
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen-3] + "..."
}

func (s *Server) generateContent(ctx context.Context, prompt *PromptPair) (string, error) {
	return s.generateContentWithSchema(ctx, prompt, nil)
}

func (s *Server) generateContentWithSchema(ctx context.Context, prompt *PromptPair, schema *genai.Schema) (string, error) {
	genaiConfig := &genai.GenerateContentConfig{}
	prompt.Config.ApplyTo(genaiConfig)
	if prompt.System != "" {
		genaiConfig.SystemInstruction = genai.NewContentFromText(prompt.System, genai.RoleUser)
	}

	if schema != nil {
		genaiConfig.ResponseMIMEType = "application/json"
		genaiConfig.ResponseSchema = schema
	}

	slog.Debug("calling LLM", "model", prompt.Config.Model, "structured", schema != nil, "prompt", prompt.User)

	resp, err := s.genaiCircuitBreaker.Execute(func() (*genai.GenerateContentResponse, error) {
		return s.genai.Models.GenerateContent(ctx, prompt.Config.Model, genai.Text(prompt.User), genaiConfig)
	})
	if err != nil {
		return "", err
	}
	slog.Debug("LLM responded", "model", prompt.Config.Model, "structured", schema != nil, "body", resp.Text())

	return strings.TrimSpace(resp.Text()), nil
}
