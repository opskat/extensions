EXT ?=

.PHONY: build test clean ci

build:
ifndef EXT
	$(error EXT is required. Usage: make build EXT=elasticsearch)
endif
	$(MAKE) -C extensions/$(EXT) build

test:
ifndef EXT
	$(error EXT is required. Usage: make test EXT=elasticsearch)
endif
	$(MAKE) -C extensions/$(EXT) test

clean:
ifndef EXT
	$(error EXT is required. Usage: make clean EXT=elasticsearch)
endif
	$(MAKE) -C extensions/$(EXT) clean

ci:
	@set -e; \
	for ext_dir in extensions/*/; do \
	  if [ -f "$$ext_dir/Makefile" ]; then \
	    ext_name=$$(basename "$$ext_dir"); \
	    echo "=== CI for $$ext_name ==="; \
	    \
	    echo "Running Go tests..."; \
	    cd "$$ext_dir" && go test ./...; \
	    cd - > /dev/null; \
	    \
	    echo "Building WASM..."; \
	    cd "$$ext_dir" && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o dist/main.wasm .; \
	    cd - > /dev/null; \
	    \
	    if [ -f "$$ext_dir/frontend/package.json" ]; then \
	      echo "Building frontend..."; \
	      cd "$$ext_dir/frontend" && pnpm install --frozen-lockfile; \
	      echo "Typechecking frontend..."; \
	      pnpm exec tsc --noEmit; \
	      echo "Running frontend tests..."; \
	      pnpm exec vitest run; \
	      echo "Building frontend bundle..."; \
	      pnpm build; \
	      cd - > /dev/null; \
	    fi; \
	  fi; \
	done; \
	echo "=== All checks passed ==="
