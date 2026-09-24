package codehealth_test

import (
	"os"
	"path/filepath"
	"testing"

	codehealth "github.com/capybari/capybari-analyzer-code-health"
	"github.com/capybari/capybari-core/analyzer"
	"github.com/capybari/capybari-core/analyzertest"
	"github.com/capybari/capybari-core/facts"
	"github.com/capybari/capybari-schemas"
	"gopkg.in/yaml.v3"
)

const fixtures = "../capybari-fixtures"

func TestCapabilityMetadata(t *testing.T) {
	b, err := os.ReadFile("capability.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := analyzer.ParseCapability(b); err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if err := schemas.ValidateValue("capability.schema.json", doc); err != nil {
		t.Fatal(err)
	}
}

func TestFixtures(t *testing.T) {
	for _, name := range []string{"node-express-legacy", "python-flask-app", "go-service"} {
		t.Run(name, func(t *testing.T) {
			r := analyzertest.Run(t, codehealth.New(), analyzertest.Repo(t, filepath.Join(fixtures, name)), analyzertest.Options{})
			sum := analyzertest.Fact[codehealth.Summary](t, r, facts.KeyCodeHealth)
			var titles []string
			for _, f := range r.Findings {
				titles = append(titles, f.Title)
			}
			analyzertest.Golden(t, name, map[string]any{"summary": sum, "findings": titles})
		})
	}
}
