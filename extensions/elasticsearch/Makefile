DIST_DIR := dist

.PHONY: build build-backend build-frontend test clean

# dist/ is a complete extension directory: install it with "install from
# directory" in the app, or `opsctl ext dev $(CURDIR)/dist`.
build: clean build-backend build-frontend
	cp manifest.json $(DIST_DIR)/
	[ ! -f SKILL.md ] || cp SKILL.md $(DIST_DIR)/
	cp -R locales $(DIST_DIR)/

build-backend:
	mkdir -p $(DIST_DIR)
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o $(DIST_DIR)/main.wasm .

# Writes $(DIST_DIR)/frontend/index.js and style.css (frontend/vite.config.ts).
build-frontend:
	cd frontend && pnpm install --frozen-lockfile && pnpm build

test:
	go test ./...
	cd frontend && pnpm install --frozen-lockfile && pnpm exec vitest run

clean:
	rm -rf $(DIST_DIR)
