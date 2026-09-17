PLUGIN_ID := workbuddy
VERSION := 0.1.0
OUT := dist

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
