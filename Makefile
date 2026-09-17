PLUGIN_ID := workbuddy
OUT := dist

# There is no version here on purpose: the version strings live in main.go
# (pluginVer — what the panel shows) and registry.json (what the store installs).
# Keep those two in step and tag the same number; this file only builds artifacts.

.PHONY: fmt test build build-local clean

fmt:
	gofmt -w main.go

test:
	go test ./...

build: fmt
	mkdir -p $(OUT)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -buildmode=c-shared -o $(OUT)/$(PLUGIN_ID)-linux-amd64.so .
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 go build -buildmode=c-shared -o $(OUT)/$(PLUGIN_ID)-linux-arm64.so .
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build -buildmode=c-shared -o $(OUT)/$(PLUGIN_ID)-darwin-amd64.dylib .
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -buildmode=c-shared -o $(OUT)/$(PLUGIN_ID)-darwin-arm64.dylib .

build-local: fmt
	mkdir -p $(OUT)
	go build -buildmode=c-shared -o $(OUT)/$(PLUGIN_ID)-local.dylib .

clean:
	rm -rf $(OUT)
