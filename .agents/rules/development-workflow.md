---
description: "Development workflow, branching, and code review conventions. Use when working on branches, PRs, or code reviews."
applyTo: "**"
---

# Development Workflow

## Branch Naming Conventions

- `feat/` - New features
- `fix/` - Bug fixes
- `docs/` - Documentation updates
- `chore/` - Maintenance tasks
- `test/` - Test-related changes
- `refactor/` - Code refactoring
- `perf/` - Performance improvements

## Code Review Guidelines

### For Authors

1. Self-review first, run tests and linters locally
2. Keep PRs focused — one feature/fix per PR
3. Write descriptive PR descriptions (what and why)
4. Respond promptly to feedback

### For Reviewers

1. Review for correctness, performance, and maintainability
2. Check test coverage for new features
3. Verify documentation for exported functions
4. Ensure proper error handling
5. Be constructive — explain the "why" behind suggestions

## Pre-commit Checklist

- [ ] Code compiles without errors
- [ ] All tests pass (`make test`)
- [ ] Linters pass (`make lint`)
- [ ] New code has tests (aim for >80% coverage)
- [ ] Exported functions are documented
- [ ] No debug code or commented-out code
- [ ] Git commit message follows convention
