GO ?= go

BIN     := herdr-smartnav
RELEASE := release
VERSION := $(shell grep '^version = ' herdr-plugin.toml | head -1 | sed 's/.*"\(.*\)".*/\1/')

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build vet fmt test clean release release-clean bump-version publish

hooks:
	git config core.hooksPath .githooks

build: hooks
	$(GO) build -o $(BIN) .

fmt:
	$(GO)fmt -w .

vet:
	$(GO) vet ./...

test: hooks build
	python3 test/harness.py

release:
	@mkdir -p $(RELEASE)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		dir=$(RELEASE)/$(BIN); \
		rm -rf $$dir; \
		mkdir -p $$dir; \
		bin=$$dir/$(BIN)-$$os-$$arch; \
		cp herdr-plugin.toml $$dir/; \
		echo "=> building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -o $$bin .; \
		tar -czf $(RELEASE)/$(BIN)-v$(VERSION)-$$os-$$arch.tar.gz -C $(RELEASE) $(BIN); \
		rm -rf $$dir; \
	done
	@echo "=> done: $(RELEASE)/$(BIN)-v$(VERSION)-*.tar.gz"

release-clean:
	rm -rf $(RELEASE)

bump-version:
	@if [ -z "$(VERSION)" ]; then \
		echo "usage: make bump-version VERSION=x.y.z"; \
		exit 1; \
	fi
	@sed -i 's/^version = ".*"/version = "$(VERSION)"/' herdr-plugin.toml
	@git add herdr-plugin.toml
	@git commit -m "bump version to $(VERSION)"
	@git tag -a "v$(VERSION)" -m "v$(VERSION)"
	@echo "=> bumped to $(VERSION), committed, tagged v$(VERSION)"

publish: release
	gh release create "v$(VERSION)" $(RELEASE)/$(BIN)-v$(VERSION)-*.tar.gz \
		--title "v$(VERSION)" --notes ""

clean:
	rm -f $(BIN)