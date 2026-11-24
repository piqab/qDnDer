package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	s, err := Load(t.TempDir())
	if s != nil || err != nil {
		t.Fatalf("missing file must give nil spec, got %v %v", s, err)
	}
}

func TestLoadAndDefaults(t *testing.T) {
	dir := t.TempDir()
	yaml := "image: reg.example.com/app\ntags: [\"{tag}\", latest]\nbuild_args:\n  VERSION: \"{tag}\"\nsteps:\n  - run: echo hi\n  - name: notify\n    run: ./notify.sh\n    when: after\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Image != "reg.example.com/app" || len(s.Tags) != 2 || s.BuildArgs["VERSION"] != "{tag}" {
		t.Fatalf("spec: %+v", s)
	}
	if s.Steps[0].When != "before" || s.Steps[0].Name != "step-1" {
		t.Fatalf("step defaults: %+v", s.Steps[0])
	}
	if s.Steps[1].When != "after" || s.Steps[1].Name != "notify" {
		t.Fatalf("step 2: %+v", s.Steps[1])
	}
}

func TestLoadInvalid(t *testing.T) {
	dir := t.TempDir()
	for _, yaml := range []string{
		"steps:\n  - name: x\n",              // no run
		"steps:\n  - run: x\n    when: sideway\n", // bad when
		"tags: [unclosed\n",                  // bad yaml
	} {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Fatalf("must reject: %q", yaml)
		}
	}
}
