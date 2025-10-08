# Makefile for mebo project
# Usage: make [target]

# Configuration
TEST_TIMEOUT    := 5m
LINT_TIMEOUT    := 5m
COVERAGE_DIR    := ./.coverage
COVERAGE_OUT    := $(COVERAGE_DIR)/coverage.out
COVERAGE_HTML   := $(COVERAGE_DIR)/coverage.html

# Source files
ALL_GO_FILES    := $(shell find . -name "*.go" -not -path "./_tests/fbs_compare/*" -not -path "./vendor/*")
TEST_DIRS       := $(sort $(dir $(shell find . -name "*_test.go" -not -path "./_tests/fbs_compare/*" -not -path "./vendor/*")))
LATEST_GIT_TAG  := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

# Default target
.DEFAULT_GOAL := help

.PHONY: test test-race test-short coverage coverage-html lint fmt vet bench clean gomod-tidy update-pkg-cache ci

## test: Run all tests with race detector and CGO disabled
test: clean-test-results
	@echo "Running tests with race detector..."
	@CGO_ENABLED=1 go test $(TEST_DIRS) -short -timeout=$(TEST_TIMEOUT) -race || (echo "Tests failed with race detector" && exit 1)
	@echo "Running tests with CGO_ENABLED=0..."
	@CGO_ENABLED=0 go test $(TEST_DIRS) -short -timeout=$(TEST_TIMEOUT) || (echo "Tests failed with CGO disabled" && exit 1)
	@echo "All tests passed!"

## test-race: Run tests with race detector only
test-race: clean-test-results
	@echo "Running tests with race detector..."
	@CGO_ENABLED=1 go test ./... -race -timeout=$(TEST_TIMEOUT)

## test-short: Run short tests only
test-short: clean-test-results
	@echo "Running short tests..."
	@go test ./... -short -timeout=$(TEST_TIMEOUT)

## coverage: Generate test coverage report
coverage: clean-test-results
	@mkdir -p $(COVERAGE_DIR)
	@echo "Generating coverage report..."
	@go test ./... -coverprofile=$(COVERAGE_OUT) -covermode=atomic -timeout=$(TEST_TIMEOUT)
	@go tool cover -func=$(COVERAGE_OUT) | tail -1

## coverage-html: Generate HTML coverage report and open in browser
coverage-html: coverage
	@echo "Generating HTML coverage report..."
	@go tool cover -html=$(COVERAGE_OUT) -o $(COVERAGE_HTML)
	@echo "Coverage report generated: $(COVERAGE_HTML)"

## bench: Run benchmarks
bench:
	@echo "Running benchmarks..."
	@go test ./... -bench=. -benchmem -run=^$$ -timeout=$(TEST_TIMEOUT)

## clean-test-results: Clean test artifacts
clean-test-results:
	@rm -f test.log *.pprof
	@rm -rf $(COVERAGE_DIR)
	@go clean -testcache

##@ Code Quality

## lint: Run linters
lint:
	@echo "Running linters..."
	@golangci-lint run --timeout=$(LINT_TIMEOUT)

## fmt: Format code
fmt:
	@echo "Formatting code..."
	@gofmt -s -w .
	@goimports -w $(ALL_GO_FILES)

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

##@ Build & Dependencies

## gomod-tidy: Tidy go.mod and go.sum
gomod-tidy:
	@echo "Tidying go modules..."
	@go mod tidy
	@go mod verify

## update-pkg-cache: Update Go package cache with latest git tag
update-pkg-cache:
	@echo "Updating package cache with latest git tag: $(LATEST_GIT_TAG)"
	@curl -sf https://proxy.golang.org/github.com/arloliu/mebo/@v/$(LATEST_GIT_TAG).info > /dev/null || \
		echo "Warning: Failed to update package cache"

##@ Cleanup

## clean: Clean all build artifacts and caches
clean: clean-test-results
	@echo "Cleaning build artifacts..."
	@go clean -cache -modcache -i -r
	@rm -rf dist/ bin/

##@ CI/CD

## ci: Run all CI checks (lint, test, coverage)
ci: lint vet test coverage
	@echo "All CI checks passed!"
