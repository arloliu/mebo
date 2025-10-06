
TEST_TIMEOUT    := 5m
LINT_TIMEOUT    := 5m
ALL_SRC         := $(shell find . -name "*.go")
ALL_SRC         += go.mod
TEST_DIRS       := $(sort $(dir $(filter %_test.go,$(ALL_SRC))))
LATEST_GIT_TAG  := $(shell git describe --tags --abbrev=0)

.PHONY: test lint clean-test-results gomod-tidy update-pkg-cache

test: clean-test-results
	@echo "Run tests in parallel with checking race condition..."
	@CGO_ENABLED=1 go test ./... -short -timeout=$(TEST_TIMEOUT) -race | tee -a test.log
	@! grep -q "^--- FAIL" test.log

	@echo "Run tests in parallel with CGO_ENABLED=0..."
	@CGO_ENABLED=0 go test ./... -short -timeout=$(TEST_TIMEOUT) | tee -a test.log
	@! grep -q "^--- FAIL" test.log

clean-test-results:
	@rm -f test.log *.pprof
	@go clean -testcache

lint:
	@golangci-lint run

gomod-tidy:
	@go mod tidy

update-pkg-cache:
	@printf "Update package cache with latest git tag: $(LATEST_GIT_TAG)\n"
	@curl -s https://proxy.golang.org/github.com/arloliu/memo/@v/$(LATEST_GIT_TAG).info > /dev/null
