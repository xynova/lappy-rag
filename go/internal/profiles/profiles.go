package profiles

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChunkStrategy controls how a document is split into index chunks.
type ChunkStrategy string

const (
	StrategyMarkdown     ChunkStrategy = "markdown"
	StrategyCodeMarkdown ChunkStrategy = "code_markdown"
	StrategyFull         ChunkStrategy = "full_document"
)

// Config holds the set of named chunk profiles.
type Config struct {
	Profiles []Profile `yaml:"profiles"`
}

// Profile maps path prefixes to chunking rules.
type Profile struct {
	Name    string      `yaml:"name" json:"name"`
	Rules   []Rule      `yaml:"rules" json:"rules"`
	Default DefaultRule `yaml:"default" json:"default"`
}

// Rule maps a path prefix to a specific doc type and chunk strategy.
type Rule struct {
	MatchPathPrefix string        `yaml:"match_path_prefix" json:"match_path_prefix"`
	DocType         string        `yaml:"doc_type" json:"doc_type"`
	ChunkStrategy   ChunkStrategy `yaml:"chunk_strategy" json:"chunk_strategy"`
}

// DefaultRule specifies the fallback doc type and chunk strategy for a profile.
type DefaultRule struct {
	DocType       string        `yaml:"doc_type" json:"doc_type"`
	ChunkStrategy ChunkStrategy `yaml:"chunk_strategy" json:"chunk_strategy"`
}

// Load reads and parses a profiles config file from the given path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied config
	if err != nil {
		return nil, fmt.Errorf("read profiles config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse profiles config: %w", err)
	}

	return &cfg, nil
}

// Find returns the named profile from the config.
func (c *Config) Find(name string) (Profile, error) {
	if c == nil {
		return Profile{}, fmt.Errorf("profiles config is nil")
	}

	for _, profile := range c.Profiles {
		if profile.Name == name {
			return profile, nil
		}
	}

	return Profile{}, fmt.Errorf("profile %q not found", name)
}

// Match returns the doc type and chunk strategy for the given relative path.
func (p Profile) Match(relativePath string) (docType string, chunkStrategy ChunkStrategy) {
	cleanPath := strings.ReplaceAll(relativePath, "\\", "/")

	for _, rule := range p.Rules {
		if strings.HasPrefix(cleanPath, rule.MatchPathPrefix) {
			return rule.DocType, rule.ChunkStrategy
		}
	}

	return p.Default.DocType, p.Default.ChunkStrategy
}
