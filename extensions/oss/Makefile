DIST_DIR := dist

.PHONY: build clean build-backend build-frontend

build: clean build-backend build-frontend
	cp manifest.json $(DIST_DIR)/
	cp -r locales $(DIST_DIR)/

build-backend:
	mkdir -p $(DIST_DIR)
	cd backend && GOOS=wasip1 GOARCH=wasm go build -o ../$(DIST_DIR)/main.wasm .

build-frontend:
	mkdir -p $(DIST_DIR)/frontend
	cd frontend && pnpm install --frozen-lockfile && pnpm build

clean:
	rm -rf $(DIST_DIR)
