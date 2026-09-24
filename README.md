# capybari-analyzer-code-health

**Capybari Source Intelligence: Code Health: where is the code hard to change?**

- **Function metrics:** cyclomatic complexity, length, nesting depth, parameter count
  - **Go:** exact, via `go/ast`
  - **JavaScript/TypeScript, Java, C#, C/C++, Kotlin, Swift, Rust, PHP, Scala, Dart, Python, Ruby:** lexical analysis that ignores comments and strings. Close to parser accuracy on typical code, and reported as medium confidence.
- **Large files**
- **Duplicated code:** CPD-style detection of blocks of 8+ normalised significant lines, extended to maximal matches, plus the codebase's duplicated-line percentage
- **FIXME/HACK/XXX markers**
- **Change hotspots:** files that are both complex and changed often in the last 12 months of git history

Findings are capped at 10 per rule, most severe first. That follows the plan's rule: *actionability over volume*.

| | |
|---|---|
| Requires | `inventory` |
| Provides | `code-health` evidence (distribution, top functions, hotspots) |
| Scores | Maintainability |
| Network / AI | none / none |

```bash
go run ./cmd/capybari-code-health ./path/to/project
```

## License

Apache-2.0
