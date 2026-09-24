package codehealth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

// Function is one measured function or method.
type Function struct {
	Name       string `json:"name"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Lines      int    `json:"lines"`
	Complexity int    `json:"complexity"`
	MaxNesting int    `json:"max_nesting"`
	Params     int    `json:"params"`
	Precise    bool   `json:"precise"` // parsed with a real parser (Go) vs lexical heuristics
}

// goFunctions measures Go functions precisely with go/ast.
func goFunctions(src []byte) []Function {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out []Function
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		name := fd.Name.Name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			name = recvName(fd.Recv.List[0].Type) + "." + name
		}
		start, end := fset.Position(fd.Pos()).Line, fset.Position(fd.End()).Line
		params := 0
		for _, p := range fd.Type.Params.List {
			if len(p.Names) == 0 {
				params++
			} else {
				params += len(p.Names)
			}
		}
		cc := 1
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
				cc++
			case *ast.CaseClause:
				if x.List != nil { // default: does not add a path
					cc++
				}
			case *ast.CommClause:
				if x.Comm != nil {
					cc++
				}
			case *ast.BinaryExpr:
				if x.Op == token.LAND || x.Op == token.LOR {
					cc++
				}
			}
			return true
		})
		out = append(out, Function{Name: name, StartLine: start, EndLine: end, Lines: end - start + 1,
			Complexity: cc, MaxNesting: goNesting(fd.Body, 0), Params: params, Precise: true})
		return true
	})
	return out
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	}
	return "?"
}

func goNesting(n ast.Node, depth int) int {
	best := depth
	ast.Inspect(n, func(c ast.Node) bool {
		if c == n {
			return true
		}
		var body *ast.BlockStmt
		switch x := c.(type) {
		case *ast.IfStmt:
			body = x.Body
			if x.Else != nil {
				if d := goNesting(x.Else, depth+1); d > best {
					best = d
				}
			}
		case *ast.ForStmt:
			body = x.Body
		case *ast.RangeStmt:
			body = x.Body
		case *ast.SwitchStmt:
			body = x.Body
		case *ast.TypeSwitchStmt:
			body = x.Body
		case *ast.SelectStmt:
			body = x.Body
		case *ast.FuncLit:
			body = x.Body
		default:
			return true
		}
		if d := goNesting(body, depth+1); d > best {
			best = d
		}
		return false
	})
	return best
}

// ---- Lexical analysis for brace languages ----

// braceFuncStart matches a line that begins a function/method definition in
// C-like languages. Deliberately conservative: control-flow keywords are
// excluded by the caller.
var braceFuncStart = map[string]*regexp.Regexp{
	"JavaScript": regexp.MustCompile(`(?:^|\s)(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)?\s*\(|([A-Za-z_$][\w$]*)\s*[:=]\s*(?:async\s+)?(?:function\b|\([^()]*\)\s*=>|[A-Za-z_$][\w$]*\s*=>)|^\s*(?:(?:public|private|protected|static|async|get|set)\s+)*([A-Za-z_$][\w$]*)\s*\([^;]*\)\s*\{`),
	"Java":       regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|override|open|internal|suspend|inline|virtual|async|unsafe|extern|partial|new)\s+)*[\w<>\[\],.?\s]+?\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*(?:throws [\w.,\s]+)?\{?\s*$`),
	"Kotlin":     regexp.MustCompile(`\bfun\s+(?:<[^>]+>\s*)?(?:[\w.]+\.)?([A-Za-z_]\w*)\s*\(`),
	"Swift":      regexp.MustCompile(`\bfunc\s+([A-Za-z_]\w*)\s*[(<]`),
	"Rust":       regexp.MustCompile(`\bfn\s+([A-Za-z_]\w*)\s*[(<]`),
	"PHP":        regexp.MustCompile(`\bfunction\s+&?([A-Za-z_]\w*)\s*\(`),
	"Scala":      regexp.MustCompile(`\bdef\s+([A-Za-z_]\w*)`),
	"Dart":       regexp.MustCompile(`^\s*(?:[\w<>?,\s]+\s+)?([A-Za-z_]\w*)\s*\([^;]*\)\s*(?:async\s*)?\{\s*$`),
}

var braceDecision = regexp.MustCompile(`\b(if|for|foreach|while|case|catch|except|when)\b|&&|\|\||\?\?|\?[^.:?]`)
var controlWords = regexp.MustCompile(`^\s*(?:\}?\s*else\b|if\b|for\b|foreach\b|while\b|switch\b|catch\b|return\b|do\b|try\b|synchronized\b|using\b|lock\b|new\b|throw\b|when\b)`)

func braceLanguage(lang string) (string, bool) {
	switch lang {
	case "JavaScript", "TypeScript", "Vue", "Svelte":
		return "JavaScript", true
	case "Java", "C#", "C", "C++", "Objective-C", "Groovy":
		return "Java", true
	case "Kotlin", "Swift", "Rust", "PHP", "Scala", "Dart":
		return lang, true
	}
	return "", false
}

// stripCode blanks out comments and string literals (keeping line
// structure) so braces and keywords inside them are not counted.
func stripCode(src string, hashComments bool) []string {
	var b strings.Builder
	b.Grow(len(src))
	const (
		normal = iota
		line
		block
		str
	)
	state := normal
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch state {
		case normal:
			switch {
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				state = line
				b.WriteByte(' ')
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				state = block
				b.WriteString("  ")
				i++
			case c == '#' && hashComments:
				state = line
				b.WriteByte(' ')
			case c == '"' || c == '\'' || c == '`':
				state = str
				quote = c
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		case line:
			if c == '\n' {
				state = normal
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		case block:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				state = normal
				b.WriteString("  ")
				i++
			} else if c == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		case str:
			switch {
			case c == '\\' && i+1 < len(src):
				b.WriteString("  ")
				i++
			case c == quote:
				state = normal
				b.WriteByte(c)
			case c == '\n' && quote != '`':
				state = normal // unterminated string: recover at end of line
				b.WriteByte('\n')
			case c == '\n':
				b.WriteByte('\n')
			default:
				b.WriteByte('x')
			}
		}
	}
	return strings.Split(b.String(), "\n")
}

// braceFunctions finds functions in C-like code by locating definition
// lines and matching their braces.
func braceFunctions(src []byte, lang string) []Function {
	family, _ := braceLanguage(lang)
	re := braceFuncStart[family]
	if re == nil {
		re = braceFuncStart["Java"]
	}
	lines := stripCode(string(src), lang == "PHP")
	var out []Function
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if controlWords.MatchString(l) {
			continue
		}
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		name := ""
		for _, g := range m[1:] {
			if g != "" {
				name = g
				break
			}
		}
		if name == "" {
			name = "(anonymous)"
		}
		if isKeyword(name) {
			continue
		}
		// Find the opening brace on this or the next two lines.
		open := -1
		for j := i; j < min(i+3, len(lines)); j++ {
			if k := strings.IndexByte(lines[j], '{'); k >= 0 && (j > i || k >= strings.Index(l, name)) {
				open = j
				break
			}
			if j > i && strings.Contains(lines[j], ";") {
				break
			}
		}
		if open < 0 {
			continue
		}
		depth, end, maxDepth, cc := 0, -1, 0, 1
		params := countParams(strings.Join(lines[i:open+1], " "))
		for j := open; j < len(lines) && end < 0; j++ {
			text := lines[j]
			cc += len(braceDecision.FindAllString(text, -1))
			for _, ch := range text {
				switch ch {
				case '{':
					depth++
					maxDepth = max(maxDepth, depth)
				case '}':
					depth--
					if depth == 0 {
						end = j
					}
				}
				if end >= 0 {
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		out = append(out, Function{Name: name, StartLine: i + 1, EndLine: end + 1, Lines: end - i + 1,
			Complexity: cc, MaxNesting: max(0, maxDepth-1), Params: params})
		// Continue scanning inside the body too: nested functions are
		// reported separately, as tools like lizard do.
	}
	return out
}

func countParams(sig string) int {
	open := strings.IndexByte(sig, '(')
	if open < 0 {
		return 0
	}
	depth, n, any := 0, 0, false
	for _, c := range sig[open:] {
		switch c {
		case '(', '<', '[', '{':
			depth++
		case ')', '>', ']', '}':
			depth--
			if depth == 0 {
				if any {
					n++
				}
				return n
			}
		case ',':
			if depth == 1 {
				n++
			}
		default:
			if depth == 1 && c != ' ' && c != '\t' {
				any = true
			}
		}
	}
	return n
}

var keywords = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "function": true, "new": true, "else": true, "do": true, "try": true, "typeof": true, "await": true, "super": true, "this": true}

func isKeyword(s string) bool { return keywords[s] }

// ---- Indentation languages (Python, Ruby) ----

var (
	pyDef       = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(([^)]*)`)
	rbDef       = regexp.MustCompile(`^(\s*)def\s+(?:self\.)?([A-Za-z_]\w*[?!=]?)\s*(?:\(([^)]*)\))?`)
	pyDecision  = regexp.MustCompile(`\b(if|elif|for|while|except|and|or|case)\b|\bif\b.*\belse\b`)
	rbDecision  = regexp.MustCompile(`\b(if|elsif|unless|for|while|until|when|rescue|and|or)\b|&&|\|\|`)
	indentWidth = func(s string) int {
		n := 0
		for _, c := range s {
			switch c {
			case ' ':
				n++
			case '\t':
				n += 4
			default:
				return n
			}
		}
		return n
	}
)

func indentFunctions(src []byte, lang string) []Function {
	lines := stripCode(string(src), true)
	raw := strings.Split(string(src), "\n")
	def, decision := pyDef, pyDecision
	if lang == "Ruby" {
		def, decision = rbDef, rbDecision
	}
	var out []Function
	for i := 0; i < len(lines); i++ {
		m := def.FindStringSubmatch(raw[i])
		if m == nil {
			continue
		}
		base := indentWidth(m[1])
		end := i
		cc, maxNest := 1, 0
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			w := indentWidth(raw[j])
			if lang == "Ruby" {
				if w <= base && (t == "end" || strings.HasPrefix(t, "end ")) {
					end = j
					break
				}
				if w <= base && !strings.HasPrefix(t, "rescue") && !strings.HasPrefix(t, "ensure") {
					break
				}
			} else if w <= base {
				break
			}
			end = j
			cc += len(decision.FindAllString(lines[j], -1))
			// Python indentation below the def body = nesting.
			if lang != "Ruby" {
				maxNest = max(maxNest, (w-base)/4-1)
			} else {
				maxNest = max(maxNest, (w-base)/2-1)
			}
		}
		params := 0
		if strings.TrimSpace(m[3]) != "" {
			for _, p := range strings.Split(m[3], ",") {
				p = strings.TrimSpace(p)
				if p != "" && p != "self" && p != "cls" && p != "*" && p != "/" {
					params++
				}
			}
		}
		out = append(out, Function{Name: m[2], StartLine: i + 1, EndLine: end + 1, Lines: end - i + 1,
			Complexity: cc, MaxNesting: max(0, maxNest), Params: params})
	}
	return out
}

// functionsFor dispatches by language. It returns nil for unsupported languages.
func functionsFor(lang string, src []byte) []Function {
	switch lang {
	case "Go":
		return goFunctions(src)
	case "Python", "Ruby":
		return indentFunctions(src, lang)
	}
	if _, ok := braceLanguage(lang); ok {
		return braceFunctions(src, lang)
	}
	return nil
}
