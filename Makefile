FIPS_ENABLED=true

include boilerplate/generated-includes.mk

.PHONY: boilerplate-update
boilerplate-update:
	@boilerplate/update

# Define variables
# The directories to test (e.g., all packages in the current dir and sub-dirs)
PKGS = ./...
# The file where the coverage profile data will be written
COVERAGE_FILE = coverage.out
# The name of the HTML coverage report directory
HTML_DIR = htmlcov

# PHONY targets do not correspond to actual files, ensuring they are always run.
.PHONY: test coverage coverage-browser clean

# 1. Run tests in the terminal (fastest execution)
test:
	@echo "🧪 Running all tests..."
	go test -v $(PKGS)

# 2. Run coverage and display summary in the terminal
coverage:
	@echo "📝 Generating coverage report..."
	go test -v -coverprofile=$(COVERAGE_FILE) $(PKGS)
	go tool cover -func=$(COVERAGE_FILE)

# 3. Generate HTML report and open it in the browser
# It depends on 'coverage' to first run the tests and create the coverage file.
coverage-browser:	
	go test -v -coverprofile=$(COVERAGE_FILE) $(PKGS)
	mkdir -p $(HTML_DIR)
	go tool cover -html=$(COVERAGE_FILE) -o $(HTML_DIR)/index.html
	@$(if $(shell which sensible-browser),sensible-browser,$(if $(shell which xdg-open),xdg-open,open)) $(HTML_DIR)/index.html

# 4. Clean up generated files
clean:
	@echo "🧹 Cleaning up test and coverage files..."
	rm -f $(COVERAGE_FILE)
	rm -rf $(HTML_DIR)