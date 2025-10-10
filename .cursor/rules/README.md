# Cursor Rules for Mebo Project

This directory contains organized rule files in MDC (Markdown with Context) format that guide Cursor AI in understanding and following the coding standards, conventions, and best practices for the Mebo project.

## MDC Format Benefits

Using `.mdc` format provides:

- **Contextual Application**: Rules apply only to relevant files via glob patterns
- **Priority Control**: Numerical prefixes control rule application order
- **Selective Loading**: Better performance by loading only relevant rules
- **Version Control**: Track changes to specific rule sets independently

## File Organization

Each MDC file has frontmatter specifying when it applies:

| File | Priority | Applies To | Description |
|------|----------|------------|-------------|
| [`001-project-overview.mdc`](001-project-overview.mdc) | Always | All files | Project context, architecture, domain knowledge |
| [`010-go-style.mdc`](010-go-style.mdc) | 10 | `**/*.go` | Go coding standards, naming, error handling |
| [`015-file-organization.mdc`](015-file-organization.mdc) | 15 | `**/*.go` | File structure, content order, 3-file rule |
| [`020-testing.mdc`](020-testing.mdc) | 20 | `**/*_test.go` | Testing patterns, benchmarks, table-driven tests |
| [`025-documentation.mdc`](025-documentation.mdc) | 25 | `**/*.go` (non-test) | Godoc format, documentation standards |
| [`030-linting.mdc`](030-linting.mdc) | 30 | `**/*.go` | golangci-lint rules, quality standards |
| [`040-performance-security.mdc`](040-performance-security.mdc) | 40 | Hot paths only | Performance optimization, security guidelines |
| [`050-development-workflow.mdc`](050-development-workflow.mdc) | Always | All files | Branching, commits, code review, releases |

## Rule Application Examples

### Writing Production Code

When editing `blob/numeric_encoder.go`:
- ✅ `001-project-overview.mdc` - Project context
- ✅ `010-go-style.mdc` - Go coding standards
- ✅ `015-file-organization.mdc` - File structure
- ✅ `025-documentation.mdc` - Godoc format
- ✅ `030-linting.mdc` - Linter rules
- ✅ `040-performance-security.mdc` - **Performance critical!**
- ✅ `050-development-workflow.mdc` - Workflow conventions
- ❌ `020-testing.mdc` - Not a test file

### Writing Tests

When editing `blob/numeric_encoder_test.go`:
- ✅ `001-project-overview.mdc` - Project context
- ✅ `010-go-style.mdc` - Go coding standards
- ✅ `015-file-organization.mdc` - File structure
- ✅ `020-testing.mdc` - **Testing guidelines!**
- ✅ `030-linting.mdc` - Linter rules (with test exceptions)
- ✅ `050-development-workflow.mdc` - Workflow conventions
- ❌ `025-documentation.mdc` - Tests don't need full godoc
- ❌ `040-performance-security.mdc` - Test files excluded

### Writing Documentation

When editing `README.md` or `docs/`:
- ✅ `001-project-overview.mdc` - Project context
- ✅ `050-development-workflow.mdc` - Workflow conventions
- ❌ Other rules don't apply to non-Go files

## Quick Reference by Task

### Writing Code
- **Style & Naming**: See [`010-go-style.mdc`](010-go-style.mdc)
- **File Structure**: See [`015-file-organization.mdc`](015-file-organization.mdc)
- **Hot Path Optimization**: See [`040-performance-security.mdc`](040-performance-security.mdc)

### Writing Tests
- **Test Patterns**: See [`020-testing.mdc`](020-testing.mdc)
- **When to use table-driven**: Multiple test cases only
- **Benchmarks**: Always use `b.Loop()` (Go 1.24+)

### Documenting
- **Godoc Format**: See [`025-documentation.mdc`](025-documentation.mdc)
- **Include**: Parameters, Returns, Example sections
- **Start with**: Function name in description

### Committing
- **Commit Format**: See [`050-development-workflow.mdc`](050-development-workflow.mdc)
- **Use**: `type(scope): subject` format
- **Run**: `make lint` and `make test` first

### Code Review
- **Guidelines**: See [`050-development-workflow.mdc`](050-development-workflow.mdc)
- **Check**: Correctness, tests, documentation, error handling

## Rule Frontmatter Structure

Each `.mdc` file has YAML frontmatter:

```yaml
---
description: Brief description of what this rule covers
globs: ["**/*.go", "!**/*_test.go"]  # File patterns to match
alwaysApply: false  # Set to true for universal rules
---
```

### Glob Pattern Examples

- `**/*.go` - All Go files recursively
- `**/*_test.go` - All test files
- `blob/**/*.go` - All Go files in blob package
- `!**/*_test.go` - Exclude test files
- Multiple patterns: `["**/*.go", "!**/*_bench_test.go"]`

## Updating Rules

When project conventions change:

1. Edit the relevant `.mdc` file
2. Update frontmatter if glob patterns change
3. Keep descriptions current
4. Test that rules apply correctly
5. Commit with clear message about what changed

## Migration from .md to .mdc

This organized structure replaced simple `.md` files with contextual `.mdc` files:

**Before**: All rules loaded for all files
**After**: Only relevant rules loaded based on file type and location

**Benefits**:
- ⚡ **Faster**: Less context for AI to process
- 🎯 **More relevant**: Rules match the code being edited
- 📦 **Better organized**: Clear priorities and application scope
- 🔧 **Easier maintenance**: Update specific rules without affecting others

## Related Documentation

- Original source: [`.github/copilot-instructions.md`](../../.github/copilot-instructions.md)
- Project README: [`README.md`](../../README.md)
- API stability: [`API_STABILITY.md`](../../API_STABILITY.md)
- Changelog: [`CHANGELOG.md`](../../CHANGELOG.md)

## How Cursor Uses These Rules

Cursor AI automatically:
1. Reads all `.mdc` files in `.cursor/rules/`
2. Evaluates glob patterns for the current file
3. Applies matching rules in priority order (001, 010, 015...)
4. Uses `alwaysApply: true` rules regardless of file type
5. Generates code following the applicable guidelines

## Questions or Issues

If you find issues with the rules or need clarification:

1. Check the specific MDC file for your use case
2. Review glob patterns to ensure rules apply correctly
3. Look at the original copilot-instructions.md for context
4. Open an issue or discussion in the project repository
