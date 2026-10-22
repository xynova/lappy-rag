package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	derrors "github.com/xynova/lappy-rag/internal/errors"
	"github.com/xynova/lappy-rag/internal/profiles"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Projects []Project `yaml:"projects"`
}

type VolumePaths struct {
	Host      string `yaml:"host" json:"host"`
	Container string `yaml:"container" json:"container"`
}

type ProjectPaths struct {
	Volume VolumePaths `yaml:"volume" json:"volume"`
}

type ChatPrompts struct {
	System              string `yaml:"system" json:"system"`
	SearchDescription   string `yaml:"search_description" json:"search_description"`
	SearchQParam        string `yaml:"search_q_param" json:"search_q_param"`
	SearchFilterParam   string `yaml:"search_filter_param" json:"search_filter_param"`
	SearchIndexUIDParam string `yaml:"search_index_uid_param" json:"search_index_uid_param"`
}

type ChatConfig struct {
	Workspace string      `yaml:"workspace" json:"workspace"`
	Persona   string      `yaml:"persona" json:"persona"`
	Source    string      `yaml:"source" json:"source"`
	BaseURL   string      `yaml:"base_url" json:"base_url"`
	APIKey    string      `yaml:"api_key" json:"api_key"`
	Model     string      `yaml:"model" json:"model"`
	Prompts   ChatPrompts `yaml:"prompts" json:"prompts"`
}

type Project struct {
	ID                string               `yaml:"id" json:"id"`
	Paths             ProjectPaths         `yaml:"paths" json:"paths"`
	HostPath          string               `yaml:"host_path" json:"host_path"`
	ContentPath       string               `yaml:"content_path" json:"content_path"`
	AllowedExtensions []string             `yaml:"allowed_extensions" json:"allowed_extensions"`
	IndexName         string               `yaml:"index_name" json:"index_name"`
	Chat              ChatConfig           `yaml:"chat" json:"chat"`
	Rules             []profiles.Rule      `yaml:"rules" json:"rules"`
	Default           profiles.DefaultRule `yaml:"default" json:"default"`
	Enabled           bool                 `yaml:"enabled" json:"enabled"`
	FollowSymlinks    bool                 `yaml:"follow_symlinks" json:"follow_symlinks"`
}

// LoadMany loads projects from one or more comma-separated paths.
// Each path may be a directory (all *.yaml/*.yml files inside are loaded recursively)
// or a single YAML file.
// Duplicate project IDs across all paths are rejected.
func LoadMany(pathList string) (*Config, error) {
	paths := splitPaths(pathList)
	if len(paths) == 0 {
		return &Config{}, nil
	}

	merged := &Config{}
	seenIDs := make(map[string]string)

	for _, bundlePath := range paths {
		if _, statErr := os.Stat(bundlePath); statErr != nil {
			if os.IsNotExist(statErr) {
				return nil, derrors.Wrap(statErr, derrors.CodeNotFound, "registry.LoadMany", fmt.Sprintf("bundle path does not exist: %s", bundlePath))
			}
			return nil, derrors.Wrap(statErr, derrors.CodeUnavailable, "registry.LoadMany", fmt.Sprintf("stat bundle path %s", bundlePath))
		}

		cfg, err := Load(bundlePath)
		if err != nil {
			return nil, fmt.Errorf("load bundle path %s: %w", bundlePath, err)
		}

		for _, project := range cfg.Projects {
			if firstPath, exists := seenIDs[project.ID]; exists {
				return nil, fmt.Errorf("duplicate project id %q found in bundle %q (first seen in %q)", project.ID, bundlePath, firstPath)
			}
			seenIDs[project.ID] = bundlePath
			merged.Projects = append(merged.Projects, project)
		}
	}

	return merged, nil
}

// splitPaths splits a comma-separated path list, trimming whitespace.
func splitPaths(pathList string) []string {
	parts := strings.Split(pathList, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read projects config %q: %w", path, err)
	}

	if info.IsDir() {
		return loadFromDir(path)
	}

	return loadFromFile(path)
}

func loadFromDir(dirPath string) (*Config, error) {
	files := make([]string, 0, 16)
	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if path == dirPath {
				return nil
			}

			name := strings.ToLower(d.Name())
			if strings.HasSuffix(name, ".example") || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}

			return nil
		}

		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read projects config directory: %w", err)
	}

	sort.Strings(files)

	projects := make([]Project, 0, len(files))
	seenIDs := make(map[string]string, len(files))
	for _, filePath := range files {
		project, readErr := readProjectFile(filePath)
		if readErr != nil {
			return nil, readErr
		}

		if firstPath, exists := seenIDs[project.ID]; exists {
			return nil, fmt.Errorf("duplicate project id %q in %s and %s", project.ID, firstPath, filePath)
		}
		seenIDs[project.ID] = filePath
		projects = append(projects, project)
	}

	return &Config{Projects: projects}, nil
}

func loadFromFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied bundle config
	if err != nil {
		return nil, fmt.Errorf("read projects config file: %w", err)
	}

	project, err := parseProject(raw)
	if err == nil {
		return &Config{Projects: []Project{project}}, nil
	}

	var cfg Config
	if parseErr := yaml.Unmarshal(raw, &cfg); parseErr != nil {
		return nil, fmt.Errorf("parse projects config file: %w", parseErr)
	}

	return &cfg, nil
}

func readProjectFile(path string) (Project, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied bundle config
	if err != nil {
		return Project{}, fmt.Errorf("read project file %s: %w", path, err)
	}

	project, err := parseProject(raw)
	if err != nil {
		return Project{}, fmt.Errorf("parse project file %s: %w", path, err)
	}

	return project, nil
}

func parseProject(raw []byte) (Project, error) {
	var project Project
	if err := yaml.Unmarshal(raw, &project); err != nil {
		return Project{}, err
	}

	if strings.TrimSpace(project.ID) == "" {
		return Project{}, fmt.Errorf("project id is required")
	}

	if strings.TrimSpace(project.IndexName) == "" {
		return Project{}, fmt.Errorf("project index_name is required")
	}

	if strings.TrimSpace(project.Default.DocType) == "" {
		return Project{}, fmt.Errorf("project default.doc_type is required")
	}

	if strings.TrimSpace(string(project.Default.ChunkStrategy)) == "" {
		return Project{}, fmt.Errorf("project default.chunk_strategy is required")
	}

	if strings.TrimSpace(project.ScanPath()) == "" {
		return Project{}, fmt.Errorf("could not derive a scan path for project %q", project.ID)
	}

	return project, nil
}

// ScanPath returns the effective path used by the ingest worker.
// Precedence: explicit paths.volume.container > legacy content_path/host_path >
// derived default of /content/projects/<id>.
func (p Project) ScanPath() string {
	if path := strings.TrimSpace(p.Paths.Volume.Container); path != "" {
		return path
	}

	if path := strings.TrimSpace(p.ContentPath); path != "" {
		return path
	}

	if path := strings.TrimSpace(p.HostPath); path != "" {
		return path
	}

	// Default: derive from project id so bundle configs need not repeat the path.
	return "/content/projects/" + strings.TrimSpace(p.ID)
}

// AllowedFileExtensions returns normalized extensions used for file inclusion.
// When not configured, it defaults to markdown-only indexing.
func (p Project) AllowedFileExtensions() []string {
	raw := p.AllowedExtensions
	if len(raw) == 0 {
		return []string{".md"}
	}

	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, ext := range raw {
		normalized := normalizeExtension(ext)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	if len(result) == 0 {
		return []string{".md"}
	}

	return result
}

func normalizeExtension(ext string) string {
	value := strings.ToLower(strings.TrimSpace(ext))
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "." + value
	}
	return value
}

func (p Project) Match(relativePath string) (docType string, chunkStrategy profiles.ChunkStrategy) {
	profile := profiles.Profile{
		Rules:   p.Rules,
		Default: p.Default,
	}

	return profile.Match(relativePath)
}

func (c *Config) Get(id string) (Project, error) {
	if c == nil {
		return Project{}, fmt.Errorf("projects config is nil")
	}

	for _, project := range c.Projects {
		if project.ID == id {
			if !project.Enabled {
				return Project{}, fmt.Errorf("project %q is disabled", id)
			}
			return project, nil
		}
	}

	return Project{}, fmt.Errorf("project %q not found", id)
}
