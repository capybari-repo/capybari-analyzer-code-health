// Package codehealth implements the Code Health capability: complexity,
// size, nesting, parameters, duplication, work markers and churn hotspots.
package codehealth

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/capybari/capybari-core/analyzer"
	"github.com/capybari/capybari-core/facts"
	"github.com/capybari/capybari-core/finding"
	"github.com/capybari/capybari-core/fsutil"
)

//go:embed capability.yaml
var capabilityYAML []byte

var capability = analyzer.MustParseCapability(capabilityYAML)

// Thresholds. Documented in docs/methodology.md.
const (
	ccLow, ccMedium, ccHigh = 15, 30, 50
	longFnLow, longFnMed    = 80, 200
	largeFileLow, largeMed  = 1000, 3000
	nestingLow              = 5
	paramsLow               = 7
	dupReportLines          = 12
	maxPerRule              = 10
)

// Summary is the evidence published under "code-health".
type Summary struct {
	FilesMeasured     int        `json:"files_measured"`
	Functions         int        `json:"functions"`
	AvgComplexity     float64    `json:"avg_complexity"`
	P90Complexity     int        `json:"p90_complexity"`
	MaxComplexity     int        `json:"max_complexity"`
	ComplexFunctions  int        `json:"complex_functions"`
	LongFunctions     int        `json:"long_functions"`
	SourceLines       int        `json:"source_lines"`
	DuplicatedLines   int        `json:"duplicated_lines"`
	DuplicationPct    float64    `json:"duplication_pct"`
	Clones            int        `json:"clones"`
	Markers           int        `json:"markers"`
	Hotspots          []Hotspot  `json:"hotspots,omitempty"`
	TopFunctions      []FuncRef  `json:"top_functions,omitempty"`
	ChurnWindow       string     `json:"churn_window,omitempty"`
	PreciseLanguages  []string   `json:"precise_languages,omitempty"`
	HeuristicLanguage []string   `json:"heuristic_languages,omitempty"`
}

// Hotspot is a file that is both complex and frequently changed.
type Hotspot struct {
	Path       string `json:"path"`
	Complexity int    `json:"complexity"`
	Commits    int    `json:"commits"`
	Score      int    `json:"score"`
}

// FuncRef identifies a function in the summary.
type FuncRef struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Line       int    `json:"line"`
	Complexity int    `json:"complexity"`
	Lines      int    `json:"lines"`
}

// Analyzer implements the capability.
type Analyzer struct{}

// New returns the capability.
func New() *Analyzer { return &Analyzer{} }

// Capability implements analyzer.Analyzer.
func (*Analyzer) Capability() analyzer.Capability { return capability }

// nonCode lists markup, style and template languages. Their size and
// repetition say little about maintainability, so they are not measured.
var nonCode = map[string]bool{
	"HTML": true, "CSS": true, "SCSS": true, "Sass": true, "Less": true, "XML": true,
	"Twig": true, "Handlebars": true, "EJS": true, "Jinja": true, "Liquid": true, "Razor": true, "MDX": true,
}

var markerRe = regexp.MustCompile(`\b(FIXME|HACK|XXX)\b`)

type fnAt struct {
	path string
	lang string
	Function
}

// Analyze implements analyzer.Analyzer.
func (*Analyzer) Analyze(ctx context.Context, in *analyzer.Input) (*analyzer.Result, error) {
	var inv facts.Inventory
	if _, err := in.Evidence.Get(facts.KeyInventory, &inv); err != nil {
		return nil, err
	}
	root := in.Target.Root
	sum := &Summary{}
	var fns []fnAt
	var dupFiles []fileLines
	fileCC := map[string]int{}
	var largeFiles []facts.File
	markerFiles := map[string]int{}
	precise, heuristic := map[string]bool{}, map[string]bool{}

	for _, f := range inv.Filter(facts.KindSource) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if f.Lines == 0 || f.Size > fsutil.DefaultMaxRead || nonCode[f.Language] {
			continue
		}
		src, _, err := fsutil.ReadFile(root, f.Path, fsutil.DefaultMaxRead)
		if err != nil {
			continue
		}
		sum.FilesMeasured++
		sum.SourceLines += f.Lines
		if f.Lines >= largeFileLow {
			largeFiles = append(largeFiles, f)
		}
		if n := len(markerRe.FindAll(src, -1)); n > 0 {
			markerFiles[f.Path] = n
			sum.Markers += n
		}
		for _, fn := range functionsFor(f.Language, src) {
			fns = append(fns, fnAt{f.Path, f.Language, fn})
			fileCC[f.Path] += fn.Complexity
			if fn.Precise {
				precise[f.Language] = true
			} else {
				heuristic[f.Language] = true
			}
		}
		if sl := significant(src, f.Language); len(sl) >= dupWindow {
			dupFiles = append(dupFiles, fileLines{f.Path, sl})
		}
	}
	sum.PreciseLanguages = sortedKeys(precise)
	sum.HeuristicLanguage = sortedKeys(heuristic)

	var findings []finding.Finding
	findings = append(findings, functionFindings(fns, sum)...)
	findings = append(findings, fileFindings(largeFiles)...)

	clones := findClones(dupFiles)
	dupLines := map[string]map[int]bool{}
	for _, c := range clones {
		for _, inst := range c.Instances {
			if dupLines[inst.Path] == nil {
				dupLines[inst.Path] = map[int]bool{}
			}
			for l := inst.StartLine; l <= inst.EndLine; l++ {
				dupLines[inst.Path][l] = true
			}
		}
	}
	for _, m := range dupLines {
		sum.DuplicatedLines += len(m)
	}
	sum.Clones = len(clones)
	if sum.SourceLines > 0 {
		sum.DuplicationPct = round1(100 * float64(sum.DuplicatedLines) / float64(sum.SourceLines))
	}
	findings = append(findings, cloneFindings(clones)...)
	if sum.DuplicationPct >= 10 && sum.SourceLines >= 2000 {
		findings = append(findings, finding.Finding{
			Dimension: finding.DimMaintainability, Category: "duplication", Severity: finding.Medium, Confidence: finding.ConfidenceHigh,
			Title:       fmt.Sprintf("%.1f%% of source lines are duplicated", sum.DuplicationPct),
			Description: fmt.Sprintf("%d of %d source lines belong to %d duplicated blocks. Fixes must be applied several times, and copies drift apart.", sum.DuplicatedLines, sum.SourceLines, sum.Clones),
			Rule:        &finding.Rule{ID: "duplication-ratio"},
			Remediation: &finding.Remediation{Summary: "Extract the largest duplicated blocks into shared functions or modules, starting with the most-changed ones.", Automatable: false},
		})
	}

	if sum.Markers >= 10 {
		paths := sortedByValue(markerFiles)
		var ev []finding.Evidence
		for _, p := range paths[:min(5, len(paths))] {
			ev = append(ev, finding.Evidence{Location: finding.Location{Path: p}, Detail: fmt.Sprintf("%d markers", markerFiles[p])})
		}
		findings = append(findings, finding.Finding{
			Dimension: finding.DimMaintainability, Category: "work-markers", Severity: finding.Info, Confidence: finding.ConfidenceHigh,
			Title:       fmt.Sprintf("%d FIXME/HACK/XXX markers in %d files", sum.Markers, len(markerFiles)),
			Description: "Markers flag known shortcuts and defects that were never resolved.",
			Evidence:    ev,
			Rule:        &finding.Rule{ID: "work-markers"},
			Remediation: &finding.Remediation{Summary: "Triage the markers into the issue tracker and resolve or remove them.", Automatable: false},
		})
	}

	churn, window := gitChurn(ctx, root)
	sum.ChurnWindow = window
	sum.Hotspots = hotspots(fileCC, churn)
	for i, h := range sum.Hotspots {
		if i >= 5 || h.Commits < 5 || h.Complexity < 30 {
			break
		}
		findings = append(findings, finding.Finding{
			Dimension: finding.DimMaintainability, Category: "hotspot", Severity: finding.Low, Confidence: finding.ConfidenceMedium,
			Title:       "Change hotspot: " + filepath.Base(h.Path),
			Description: fmt.Sprintf("%s changed in %d commits (%s) and has a total cyclomatic complexity of %d. Complex code that changes often is where regressions and delivery delays concentrate.", h.Path, h.Commits, window, h.Complexity),
			Evidence:    []finding.Evidence{{Location: finding.Location{Path: h.Path}, Detail: fmt.Sprintf("%d commits × complexity %d", h.Commits, h.Complexity)}},
			Rule:        &finding.Rule{ID: "churn-hotspot"},
			Remediation: &finding.Remediation{Summary: "Prioritise tests and refactoring here. Improvements pay off in the files that change most.", Automatable: false},
		})
	}

	res := &analyzer.Result{
		Evidence: map[string]any{facts.KeyCodeHealth: sum},
		Findings: findings,
		Summary: fmt.Sprintf("%d functions in %d files; avg complexity %.1f, max %d; %.1f%% duplicated; %d hotspot(s)",
			sum.Functions, sum.FilesMeasured, sum.AvgComplexity, sum.MaxComplexity, sum.DuplicationPct, min(len(sum.Hotspots), 5)),
	}
	if len(sum.HeuristicLanguage) > 0 {
		res.Limitations = append(res.Limitations, fmt.Sprintf("Function metrics for %s use lexical heuristics, not a full parser; small deviations from compiler-accurate values are expected.", strings.Join(sum.HeuristicLanguage, ", ")))
	}
	if window == "" {
		res.Limitations = append(res.Limitations, "No git history available, so change hotspots could not be computed.")
	}
	return res, nil
}

func functionFindings(fns []fnAt, sum *Summary) []finding.Finding {
	sum.Functions = len(fns)
	if len(fns) == 0 {
		return nil
	}
	ccs := make([]int, len(fns))
	total := 0
	for i, f := range fns {
		ccs[i] = f.Complexity
		total += f.Complexity
		if f.Complexity > sum.MaxComplexity {
			sum.MaxComplexity = f.Complexity
		}
		if f.Complexity > ccLow {
			sum.ComplexFunctions++
		}
		if f.Lines > longFnLow {
			sum.LongFunctions++
		}
	}
	sort.Ints(ccs)
	sum.AvgComplexity = round1(float64(total) / float64(len(fns)))
	sum.P90Complexity = ccs[int(float64(len(ccs)-1)*0.9)]

	byCC := append([]fnAt(nil), fns...)
	sort.SliceStable(byCC, func(i, j int) bool { return byCC[i].Complexity > byCC[j].Complexity })
	for _, f := range byCC[:min(10, len(byCC))] {
		sum.TopFunctions = append(sum.TopFunctions, FuncRef{f.path, f.Name, f.StartLine, f.Complexity, f.Lines})
	}

	conf := func(f fnAt) finding.Confidence {
		if f.Precise {
			return finding.ConfidenceHigh
		}
		return finding.ConfidenceMedium
	}
	loc := func(f fnAt) []finding.Evidence {
		return []finding.Evidence{{Location: finding.Location{Path: f.path, StartLine: f.StartLine, EndLine: f.EndLine, Symbol: f.Name}}}
	}
	var out []finding.Finding
	emit := func(rule string, list []fnAt, mk func(fnAt) finding.Finding) {
		for i, f := range list {
			if i == maxPerRule {
				break
			}
			ff := mk(f)
			ff.Rule = &finding.Rule{ID: rule}
			out = append(out, ff)
		}
	}

	var complex []fnAt
	for _, f := range byCC {
		if f.Complexity > ccLow {
			complex = append(complex, f)
		}
	}
	emit("function-complexity", complex, func(f fnAt) finding.Finding {
		sev := finding.Low
		switch {
		case f.Complexity > ccHigh:
			sev = finding.High
		case f.Complexity > ccMedium:
			sev = finding.Medium
		}
		return finding.Finding{
			Dimension: finding.DimMaintainability, Category: "complexity", Severity: sev, Confidence: conf(f),
			Title:       fmt.Sprintf("Complex function %s (cyclomatic complexity %d)", f.Name, f.Complexity),
			Description: fmt.Sprintf("%s in %s has %d independent paths across %d lines. Each path needs a test, and changes are hard to reason about.", f.Name, f.path, f.Complexity, f.Lines),
			Evidence:    loc(f),
			Remediation: &finding.Remediation{Summary: "Split the function into smaller units, replace nested conditionals with early returns or lookup tables, and cover the paths with tests first.", Automatable: false},
		}
	})

	var long []fnAt
	for _, f := range fns {
		if f.Lines > longFnLow && f.Complexity <= ccLow { // complex ones are already reported
			long = append(long, f)
		}
	}
	sort.SliceStable(long, func(i, j int) bool { return long[i].Lines > long[j].Lines })
	emit("long-function", long, func(f fnAt) finding.Finding {
		sev := finding.Low
		if f.Lines > longFnMed {
			sev = finding.Medium
		}
		return finding.Finding{
			Dimension: finding.DimMaintainability, Category: "long-function", Severity: sev, Confidence: conf(f),
			Title:       fmt.Sprintf("Long function %s (%d lines)", f.Name, f.Lines),
			Description: fmt.Sprintf("%s in %s spans %d lines, which usually means it does several jobs.", f.Name, f.path, f.Lines),
			Evidence:    loc(f),
			Remediation: &finding.Remediation{Summary: "Extract cohesive steps into named helper functions.", Automatable: false},
		}
	})

	var deep []fnAt
	for _, f := range fns {
		if f.MaxNesting > nestingLow {
			deep = append(deep, f)
		}
	}
	sort.SliceStable(deep, func(i, j int) bool { return deep[i].MaxNesting > deep[j].MaxNesting })
	emit("deep-nesting", deep, func(f fnAt) finding.Finding {
		return finding.Finding{
			Dimension: finding.DimMaintainability, Category: "deep-nesting", Severity: finding.Low, Confidence: conf(f),
			Title:       fmt.Sprintf("Deeply nested code in %s (depth %d)", f.Name, f.MaxNesting),
			Evidence:    loc(f),
			Remediation: &finding.Remediation{Summary: "Flatten with guard clauses and extracted helpers.", Automatable: false},
		}
	})

	var params []fnAt
	for _, f := range fns {
		if f.Params > paramsLow {
			params = append(params, f)
		}
	}
	sort.SliceStable(params, func(i, j int) bool { return params[i].Params > params[j].Params })
	emit("many-parameters", params, func(f fnAt) finding.Finding {
		return finding.Finding{
			Dimension: finding.DimMaintainability, Category: "many-parameters", Severity: finding.Low, Confidence: conf(f),
			Title:       fmt.Sprintf("%s takes %d parameters", f.Name, f.Params),
			Evidence:    loc(f),
			Remediation: &finding.Remediation{Summary: "Group related parameters into a struct/object or split responsibilities.", Automatable: false},
		}
	})
	return out
}

func fileFindings(files []facts.File) []finding.Finding {
	sort.Slice(files, func(i, j int) bool { return files[i].Lines > files[j].Lines })
	var out []finding.Finding
	for i, f := range files {
		if i == maxPerRule {
			break
		}
		sev := finding.Low
		if f.Lines > largeMed {
			sev = finding.Medium
		}
		out = append(out, finding.Finding{
			Dimension: finding.DimMaintainability, Category: "large-file", Severity: sev, Confidence: finding.ConfidenceHigh,
			Title:       fmt.Sprintf("Large source file %s (%d lines)", filepath.Base(f.Path), f.Lines),
			Evidence:    []finding.Evidence{{Location: finding.Location{Path: f.Path}}},
			Rule:        &finding.Rule{ID: "large-file"},
			Remediation: &finding.Remediation{Summary: "Split the file along its responsibilities into smaller modules.", Automatable: false},
		})
	}
	return out
}

func cloneFindings(clones []Clone) []finding.Finding {
	var out []finding.Finding
	for _, c := range clones {
		if len(out) == maxPerRule {
			break
		}
		if c.Lines < dupReportLines {
			continue
		}
		var ev []finding.Evidence
		for _, inst := range c.Instances {
			ev = append(ev, finding.Evidence{Location: finding.Location{Path: inst.Path, StartLine: inst.StartLine, EndLine: inst.EndLine}})
		}
		ev[0].Snippet = c.Sample
		out = append(out, finding.Finding{
			Dimension: finding.DimMaintainability, Category: "duplication", Severity: finding.Low, Confidence: finding.ConfidenceHigh,
			Title:       fmt.Sprintf("Duplicated block of %d lines in %d places", c.Lines, len(c.Instances)),
			Evidence:    ev,
			Rule:        &finding.Rule{ID: "duplicated-block"},
			Remediation: &finding.Remediation{Summary: "Extract the shared logic once and call it from each place.", Automatable: false},
		})
	}
	return out
}

// gitChurn counts commits touching each file over the last year.
func gitChurn(ctx context.Context, root string) (map[string]int, string) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil, ""
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, ""
	}
	out, err := exec.CommandContext(ctx, git, "-C", root, "-c", "core.quotepath=off", "log", "--since=1.year", "--no-merges", "--name-only", "--format=", "-n", "5000").Output()
	if err != nil {
		return nil, ""
	}
	churn := map[string]int{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			churn[l]++
		}
	}
	return churn, "last 12 months"
}

func hotspots(fileCC, churn map[string]int) []Hotspot {
	if churn == nil {
		return nil
	}
	var hs []Hotspot
	for p, cc := range fileCC {
		if n := churn[p]; n > 0 {
			hs = append(hs, Hotspot{Path: p, Complexity: cc, Commits: n, Score: cc * n})
		}
	}
	sort.Slice(hs, func(i, j int) bool {
		if hs[i].Score != hs[j].Score {
			return hs[i].Score > hs[j].Score
		}
		return hs[i].Path < hs[j].Path
	})
	return hs[:min(len(hs), 10)]
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedByValue(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
