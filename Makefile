EXT ?=

.PHONY: build test clean

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
