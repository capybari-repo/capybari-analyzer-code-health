package codehealth

import (
	"fmt"
	"strings"
	"testing"
)

func byName(fns []Function) map[string]Function {
	m := map[string]Function{}
	for _, f := range fns {
		m[f.Name] = f
	}
	return m
}

func TestGoFunctions(t *testing.T) {
	src := `package x

type T struct{}

func simple() int { return 1 }

func (t *T) Branchy(a, b int, c string) int {
	if a > 0 && b > 0 {
		for i := 0; i < a; i++ {
			switch {
			case i == 1:
				return 1
			case i == 2 || c == "x":
				return 2
			default:
			}
		}
	}
	return 0
}
`
	fns := byName(goFunctions([]byte(src)))
	if f := fns["simple"]; f.Complexity != 1 || f.Params != 0 || !f.Precise {
		t.Fatalf("simple: %+v", f)
	}
	// 1 + if + && + for + case + case + || = 7
	b := fns["T.Branchy"]
	if b.Complexity != 7 || b.Params != 3 || b.MaxNesting != 3 || b.StartLine != 7 || b.EndLine != 20 {
		t.Fatalf("Branchy: %+v", b)
	}
}

func TestBraceFunctionsJS(t *testing.T) {
	src := "// a comment with if ( and { braces\n" +
		"function load(id, opts) {\n" +
		"  const s = \"if { not code }\";\n" +
		"  if (id && opts.fast) {\n" +
		"    return cache[id] ?? fetch(id);\n" +
		"  } else if (opts.retry) {\n" +
		"    for (const x of opts.retry) { tryIt(x); }\n" +
		"  }\n" +
		"  return null;\n" +
		"}\n" +
		"const add = (a, b) => {\n" +
		"  return a > b ? a : b;\n" +
		"};\n" +
		"class Repo {\n" +
		"  async find(q) {\n" +
		"    while (q.next()) { q.step(); }\n" +
		"  }\n" +
		"}\n"
	fns := byName(braceFunctions([]byte(src), "JavaScript"))
	// load: 1 + if + && + ?? + else-if + for = 6
	if f := fns["load"]; f.Complexity != 6 || f.Params != 2 || f.StartLine != 2 || f.EndLine != 10 {
		t.Fatalf("load: %+v (all: %+v)", f, fns)
	}
	if f := fns["add"]; f.Complexity != 2 || f.Params != 2 {
		t.Fatalf("add: %+v", f)
	}
	if f := fns["find"]; f.Complexity != 2 {
		t.Fatalf("find: %+v", f)
	}
	if _, ok := fns["if"]; ok {
		t.Fatal("control keyword detected as function")
	}
}

func TestIndentFunctionsPython(t *testing.T) {
	src := `class A:
    def to_dict(self, rate, extra=None):
        if rate and "rates" in rate:
            if "EUR" in rate["rates"]:
                if self.total is not None:
                    return 1
        return 2

    def other(self):
        return 3
`
	fns := byName(indentFunctions([]byte(src), "Python"))
	// 1 + if + and + if + if = 5
	if f := fns["to_dict"]; f.Complexity != 5 || f.Params != 2 || f.MaxNesting != 3 || f.EndLine != 7 {
		t.Fatalf("to_dict: %+v", f)
	}
	if f := fns["other"]; f.Complexity != 1 || f.Lines != 2 {
		t.Fatalf("other: %+v", f)
	}
}

func TestFindClones(t *testing.T) {
	block := func(prefix string) string {
		var b strings.Builder
		for i := range 14 {
			fmt.Fprintf(&b, "total = total + price_%d * quantity_%d\n", i, i)
		}
		return prefix + b.String()
	}
	a := significant([]byte(block("def a():\n")), "Python")
	b := significant([]byte("x = 1\n"+block("def b():\n")+"y = 2\n"), "Python")
	c := significant([]byte("unrelated = True\nnothing_here = False\n"), "Python")
	clones := findClones([]fileLines{{"a.py", a}, {"b.py", b}, {"c.py", c}})
	if len(clones) != 1 {
		t.Fatalf("clones = %+v", clones)
	}
	cl := clones[0]
	if len(cl.Instances) != 2 || cl.Lines != 14 || cl.Instances[0].Path != "a.py" || cl.Instances[1].StartLine != 3 {
		t.Fatalf("clone = %+v", cl)
	}
}

func TestHotspots(t *testing.T) {
	hs := hotspots(map[string]int{"a.go": 50, "b.go": 5, "c.go": 100}, map[string]int{"a.go": 10, "b.go": 40, "d.go": 3})
	if len(hs) != 2 || hs[0].Path != "a.go" || hs[0].Score != 500 || hs[1].Path != "b.go" {
		t.Fatalf("hotspots = %+v", hs)
	}
}
