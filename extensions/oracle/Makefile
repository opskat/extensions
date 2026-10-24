DIST_DIR := dist

.PHONY: build build-backend test clean

# dist/ is a complete extension directory: install it with "install from
# directory" in the app, or `opsctl ext dev $(CURDIR)/dist`.
build: clean build-backend
	cp manifest.json $(DIST_DIR)/
	cp SKILL.md $(DIST_DIR)/
	cp -R locales $(DIST_DIR)/

build-backend:
	mkdir -p $(DIST_DIR)
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o $(DIST_DIR)/main.wasm .

# Unit tests. The real-database tests (integration_*_test.go) run only when the
# ORACLE_TEST_* variables point at a database; see integration_helper_test.go.
test:
	go test ./...

clean:
	rm -rf $(DIST_DIR)
