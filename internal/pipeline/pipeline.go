package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the optional pipeline file in the repository root.
const FileName = "qdnder.yaml"

// Step is a shell command run before or after the image build.
type Step struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	When string `yaml:"when"` // before (default) | after
}

// Spec is the flat qdnder.yaml pipeline, styled after nkt pipeline.yaml.
// Empty fields keep the project settings.
type Spec struct {
	Dockerfile string            `yaml:"dockerfile"`
	Context    string            `yaml:"context"`
	Image      string            `yaml:"image"`
	Tags       []string          `yaml:"tags"`
	BuildArgs  map[string]string `yaml:"build_args"`
	Steps      []Step            `yaml:"steps"`
}

// Load reads qdnder.yaml from the repository root. A missing file is not an
// error (nil spec): the project settings are used as-is.
func Load(repoDir string) (*Spec, error) {
	raw, err := os.ReadFile(filepath.Join(repoDir, FileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		if st.Run == "" {
			return nil, fmt.Errorf("%s: step %d: run is required", FileName, i+1)
		}
		if st.When == "" {
			st.When = "before"
		}
		if st.When != "before" && st.When != "after" {
			return nil, fmt.Errorf("%s: step %d: when must be before|after", FileName, i+1)
		}
		if st.Name == "" {
			st.Name = fmt.Sprintf("step-%d", i+1)
		}
	}
	return &s, nil
}
