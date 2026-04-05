# Mebo — Agent Rules

This project uses structured rule files to guide AI agent behavior. All rules live in `.agents/rules/` and are organized by concern.

## Agent Skills

In addition to rules, this project defines specialized agent skills in the `.agents/skills/` directory. These skills encapsulate complex workflows, code review checklists (like QA reviews), and automated maintenance scripts.
- **Location**: `.agents/skills/`
- Agents or AI coding assistants that do not natively discover the skills folder (like GitHub Copilot) should proactively read these files when executing specialized tasks.

## Rules

| Rule | Applies To | Description |
|------|-----------|-------------|
| [project-overview](.agents/rules/project-overview.md) | All files | Project architecture, domain context, and key concepts |
| [go-style](.agents/rules/go-style.md) | `**/*.go` | Go coding standards, naming, error handling, modern idioms |
| [file-organization](.agents/rules/file-organization.md) | `**/*.go` | File content order, 3-file rule, import organization |
| [testing](.agents/rules/testing.md) | `**/*_test.go` | Table-driven tests, benchmarks (b.Loop), testify usage |
| [documentation](.agents/rules/documentation.md) | `**/*.go` | Godoc format for exported functions, types, and methods |
| [linting](.agents/rules/linting.md) | `**/*.go` | golangci-lint rules, complexity limits, safety checks |
| [performance-security](.agents/rules/performance-security.md) | `blob/`, `compress/`, `internal/encoding/` | Hot path optimization, branchless code, memory management |
| [development-workflow](.agents/rules/development-workflow.md) | All files | Branch naming, code review, pre-commit checklist |
| [commit-messages](.agents/rules/commit-messages.md) | All files | Conventional commit format, scope, subject line rules |

## Quick Reference

- **Language**: Go >=1.24.0
- **Module**: `github.com/arloliu/mebo`
- **Lint**: `make lint`
- **Test**: `make test`
- **Benchmarks**: Use `b.Loop()` (Go 1.24+), never `b.N`

## Non-Negotiable Constraints

1. **All exported symbols must have godoc** — see [documentation](.agents/rules/documentation.md)
2. **Errors must be handled explicitly** — no ignored errors, always wrap with `%w`
3. **Type assertions must use comma-ok** — `val, ok := x.(Type)`
4. **Max 3 files per component** — impl + test + bench (optional)
5. **Pre-allocate slices and maps** when size is known
6. **Hot paths must be branchless** where possible — see [performance-security](.agents/rules/performance-security.md)
7. **Commit messages follow conventional format** — `type(scope): subject`
