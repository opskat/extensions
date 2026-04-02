EXT ?=

.PHONY: build clean

build:
ifndef EXT
	$(error EXT is required. Usage: make build EXT=oss)
endif
	$(MAKE) -C extensions/$(EXT) build

clean:
ifndef EXT
	$(error EXT is required. Usage: make clean EXT=oss)
endif
	$(MAKE) -C extensions/$(EXT) clean
