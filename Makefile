.PHONY: test verify build release release-check
VERSION ?= 0.4.1
RELEASE_PLATFORMS ?= linux/amd64 linux/arm64 darwin/arm64
RELEASE_DIR ?= bin/release-$(VERSION)
build:
	mkdir -p bin
	GOWORK=off go build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/bloem-pastime ./cmd/bloem-pastime

test:
	GOWORK=off go test -count=1 ./...

verify:
	GOWORK=off go test -race -count=1 ./...
	GOWORK=off go vet ./...
	$(MAKE) build
	./bin/bloem-pastime manifest > bin/manifest.json
	python3 scripts/check-manifest.py bin/manifest.json

# Reproducible catalog binaries: stripped, no build ID, version stamped. Each
# platform also gets the manifest Silo installs for it, generated from the
# binary, and a Silo archive (manifest.json + plugin). Publish through the
# catalog; never hand-write a manifest or an archive.
release:
	mkdir -p $(RELEASE_DIR)
	for platform in $(RELEASE_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOWORK=off go build -trimpath -ldflags '-s -w -buildid= -X main.version=$(VERSION)' -o $(RELEASE_DIR)/plugin-$$os-$$arch ./cmd/bloem-pastime || exit 1; \
	done
	cd $(RELEASE_DIR) && sha256sum plugin-* > checksums.txt
	GOWORK=off go run ./scripts/releasecheck -package -dir $(RELEASE_DIR) -version $(VERSION) -platforms "$(RELEASE_PLATFORMS)"

# Fails when a manifest, archive, checksum, platform list or version disagrees
# with the binaries. CATALOG=<bloem-plugins manifest.json> also checks the
# catalog entry.
release-check:
	GOWORK=off go run ./scripts/releasecheck -dir $(RELEASE_DIR) -version $(VERSION) -platforms "$(RELEASE_PLATFORMS)" $(if $(CATALOG),-catalog $(CATALOG))
