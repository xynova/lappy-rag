package dspygo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dspycore "github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/llms"
	"github.com/XiaoConstantine/dspy-go/pkg/modules"

	"github.com/xynova/lappy-rag/internal/observability"
)

// Plan holds the primary query and its expanded variants.
type Plan struct {
	Primary string   `json:"primary"`
	Queries []string `json:"queries"`
}

// PlannerConfig holds planner construction inputs.
type PlannerConfig struct {
	BaseURL string
	Model   string
	Logger  *observability.Logger
}

// Planner builds multi-query search plans from a natural language question.
// When a LocalAI base URL and model are provided it uses a real ChainOfThought
// DSPy module; otherwise it falls back to rule-based expansion.
type Planner struct {
	cot    *modules.ChainOfThought
	logger *observability.Logger
}

// CreatePlanner builds a Planner from config.
func (cfg PlannerConfig) CreatePlanner() *Planner {
	p := &Planner{logger: cfg.Logger}
	if cfg.Logger == nil {
		p.logger = observability.NewLogger("dspygo")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	model := strings.TrimSpace(cfg.Model)
	if baseURL == "" || model == "" {
		return p
	}

	normalizedBaseURL := strings.TrimRight(baseURL, "/")
	openAIPath := "/v1/chat/completions"
	if strings.HasSuffix(normalizedBaseURL, "/v1") {
		openAIPath = "/chat/completions"
	}

	llm, err := llms.NewOpenAILLM(
		dspycore.ModelID(model),
		llms.WithOpenAIBaseURL(normalizedBaseURL),
		llms.WithOpenAIPath(openAIPath),
		llms.WithAPIKey("none"),
	)
	if err != nil {
		p.logger.Warnf("failed to init LLM planner, using rule-based fallback: %v", err)
		return p
	}

	sig := dspycore.NewSignature(
		[]dspycore.InputField{{Field: dspycore.NewField("question", dspycore.WithDescription("The user's search question."))}},
		[]dspycore.OutputField{
			{Field: dspycore.NewField("primary_query", dspycore.WithDescription("The single best search query derived from the question."))},
			{Field: dspycore.NewField("expanded_queries", dspycore.WithDescription("JSON array of 2-3 alternative search queries that cover different aspects or phrasings of the question."))},
		},
	).WithInstruction("You are a search query planner. Generate a primary query and alternative queries to maximise recall from a documentation index. Return expanded_queries as a JSON array of strings.")

	p.cot = modules.NewChainOfThought(sig)
	p.cot.SetLLM(llm)
	return p
}

// NewPlanner returns a Planner. baseURL and model configure the LLM-backed path;
// pass empty strings to use the rule-based fallback only.
func NewPlanner(baseURL, model string) *Planner {
	return PlannerConfig{BaseURL: baseURL, Model: model}.CreatePlanner()
}

// IsLLMBacked reports whether the planner is using a real LLM ChainOfThought module.
func (p *Planner) IsLLMBacked() bool {
	return p != nil && p.cot != nil
}

// Build expands question into a Plan. Uses the LLM ChainOfThought module when
// available, falling back to rule-based expansion on any error.
func (p *Planner) Build(ctx context.Context, question string) Plan {
	q := strings.TrimSpace(question)
	if q == "" {
		return Plan{}
	}

	if ctx == nil {
		return Plan{}
	}
	if p != nil && p.cot != nil {
		if _, ok := ctx.Deadline(); !ok {
			return p.buildFallback(q)
		}
		plan, err := p.buildWithLLM(ctx, q)
		if err != nil {
			if p.logger != nil {
				p.logger.Warnf("LLM planner error, falling back: %v", err)
			}
		} else {
			return plan
		}
	}

	return p.buildFallback(q)
}

// buildWithLLM runs the ChainOfThought module and parses its structured output.
func (p *Planner) buildWithLLM(ctx context.Context, question string) (Plan, error) {
	result, err := p.cot.Process(ctx, map[string]any{
		"question": question,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("cot.Process: %w", err)
	}

	primary, _ := result["primary_query"].(string)
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return Plan{}, fmt.Errorf("empty primary_query in response")
	}

	unique := map[string]struct{}{strings.ToLower(primary): {}}
	queries := []string{primary}

	raw, _ := result["expanded_queries"].(string)
	var expanded []string
	if err := json.Unmarshal([]byte(raw), &expanded); err == nil {
		for _, q := range expanded {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			k := strings.ToLower(q)
			if _, seen := unique[k]; seen {
				continue
			}
			unique[k] = struct{}{}
			queries = append(queries, q)
		}
	}

	return Plan{Primary: primary, Queries: queries}, nil
}

// buildFallback is the original rule-based query expansion.
func (p *Planner) buildFallback(q string) Plan {
	tokens := tokenize(q)
	unique := map[string]struct{}{}
	queries := make([]string, 0, 3)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		k := strings.ToLower(s)
		if _, ok := unique[k]; ok {
			return
		}
		unique[k] = struct{}{}
		queries = append(queries, s)
	}

	add(q)

	if len(tokens) > 4 {
		add(strings.Join(tokens[:4], " "))
	}

	keywords := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if len(t) >= 4 {
			keywords = append(keywords, t)
		}
		if len(keywords) == 6 {
			break
		}
	}
	if len(keywords) > 0 {
		add(strings.Join(keywords, " "))
	}

	primary := ""
	if len(queries) > 0 {
		primary = queries[0]
	}

	return Plan{Primary: primary, Queries: queries}
}

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		"\t", " ",
		",", " ",
		".", " ",
		";", " ",
		":", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
		"{", " ",
		"}", " ",
		"!", " ",
		"?", " ",
	)
	s = replacer.Replace(s)
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
