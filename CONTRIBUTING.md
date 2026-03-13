# Contributing to Klever Node Hub

Thank you for your interest in contributing to Klever Node Hub! This guide will help you get started.

## Getting Started

1. **Fork** the repository on GitHub
2. **Clone** your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/KleverNodeHub.git
   cd KleverNodeHub
   ```
3. **Create a branch** for your work:
   ```bash
   git checkout -b feat/your-feature-name
   ```

## Prerequisites

- Go 1.22+
- Docker (for testing containerized deployment)
- [golangci-lint](https://golangci-lint.run/welcome/install-local/)

## Development Workflow

### Build

```bash
make build            # Build both dashboard and agent
make build-dashboard  # Build dashboard only
make build-agent      # Build agent only
```

### Run Locally

```bash
make run              # Run the dashboard
make run-live         # Run with hot-reload (requires air)
```

### Test

All contributions must pass the full test suite:

```bash
make test             # go test ./... -v -race
make lint             # golangci-lint + go vet
make security         # govulncheck
```

### Before Submitting

1. Run tests: `go test ./... -v -race`
2. Run linter: `golangci-lint run`
3. Verify build: `go build ./cmd/dashboard && go build ./cmd/agent`
4. Format code: `gofmt -w . && goimports -w .`

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/) format:

```
type(scope): description
```

**Types:** `feat`, `fix`, `refactor`, `docs`, `test`, `chore`

**Examples:**
```
feat(agent): add node auto-discovery on registration
fix(auth): correct passkey challenge expiration
docs: update installation instructions
test(store): add metrics retention tests
```

## Pull Requests

1. Keep PRs focused — one feature or fix per PR
2. Update documentation if your change affects user-facing behavior
3. Add tests for new functionality
4. Ensure all CI checks pass before requesting review
5. Fill out the PR template completely

## Code Guidelines

### Go Conventions

- Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- Use `gofmt` and `goimports` before committing
- Handle all errors explicitly — no blank `_` for error returns
- Use table-driven tests
- Keep interfaces small (1-3 methods)

### Dependencies

This project maintains a minimal dependency footprint for security reasons. **Do not add new third-party dependencies** without prior discussion in an issue. The project relies on:

- Go standard library
- `golang.org/x/crypto`
- A small set of vetted packages (see `go.mod`)

### Security

- Never store secrets in code
- Follow the command whitelist pattern for agent operations
- Maintain mTLS for all dashboard-agent communication
- See [SECURITY.md](SECURITY.md) for reporting vulnerabilities

## Reporting Bugs

Use the [bug report template](https://github.com/CTJaeger/KleverNodeHub/issues/new?template=bug_report.md) to file issues. Include:

- Go version and OS
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs

## Requesting Features

Use the [feature request template](https://github.com/CTJaeger/KleverNodeHub/issues/new?template=feature_request.md). Describe:

- The problem you're trying to solve
- Your proposed solution
- Any alternatives you've considered

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
