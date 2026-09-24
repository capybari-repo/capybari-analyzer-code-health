# Methodology: Code Health

Only files the inventory classifies as **source** are measured. Tests, generated, vendored and data files are excluded.

## Cyclomatic complexity

`1 +` decision points: `if`, `for`/`foreach`/`while`, `case` (not `default`), `catch`/`except`/`rescue`, `&&`/`||`/`and`/`or`, `??`, ternary `?`. Go additionally counts `select` cases, and is measured on the AST.

| Complexity | Severity | Rationale |
|---|---|---|
| 16–30 | low | above the common review threshold (15) |
| 31–50 | medium | high risk: hard to test exhaustively |
| > 50 | high | effectively untestable |

## Other function rules

| Rule | Trigger | Severity |
|---|---|---|
| `long-function` | > 80 lines (and not already complex) | low; > 200 lines medium |
| `deep-nesting` | nesting depth > 5 | low |
| `many-parameters` | > 7 parameters | low |
| `large-file` | ≥ 1,000 lines | low; > 3,000 lines medium |

## Duplication

Comments and strings are blanked, whitespace is collapsed, and blank, brace-only and import lines are dropped. Every window of 8 significant lines is hashed. Identical windows in two or more places are verified textually and extended to maximal blocks. Blocks of ≥ 12 original lines become findings (top 10 by size × copies). If ≥ 10 % of source lines are duplicated (in codebases of ≥ 2,000 lines), a medium finding summarises it.

## Hotspots

`hotspot score = total file complexity × commits touching the file in the last 12 months` (`git log --since=1.year`). A file with ≥ 5 commits and complexity ≥ 30 in the top 5 produces a low finding. Hotspots are where refactoring and tests pay off first.

## Confidence

Go findings are high confidence (parser). Lexically measured languages are medium.

## Contribution to scores

Maintainability. Findings are capped at 10 per rule, so large codebases are not penalised for their size alone.
