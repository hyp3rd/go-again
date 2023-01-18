# test runs the Go tests for the current package and the tests package.
test:
	go test -v ./...

# bench runs the benchmark tests in the benchmark subpackage of the tests package.
bench:
	go test -bench=. -benchmem -benchtime=4s . -timeout 30m

# vet runs the Go vet static analysis tool on all packages in the project.
vet:
	go vet -v ./...

# lint runs the staticcheck and golint static analysis tools on all packages in the project.
lint:
	$(call check_command_exists,staticcheck) && staticcheck ./...
	$(call check_command_exists,golint) || go install -v golang.org/x/lint/golint 
	golint ./...

# check_command_exists is a helper function that checks if a command exists.
check_command_exists = $(shell command -v $(1) > /dev/null && echo "true" || echo "false")

ifeq ($(call check_command_exists,$(1)),false)
  $(error "$(1) command not found")
endif

# help prints a list of available targets and their descriptions.
help:
	@echo "Available targets:"
	@echo
	@echo "test      Run Go tests for the current package."
	@echo "bench     Run benchmark tests"
	@echo "vet       Run the Go vet static analysis tool on all packages in the project."
	@echo "lint      Run the staticcheck and golint static analysis tools on all packages in the project."
	@echo "help      Print this help message."
