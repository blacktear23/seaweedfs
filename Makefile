BINARY = weed

SOURCE_DIR = .

.PHONY : test

all: install

install:
	cd weed; go install

full_install:
	cd weed; go install -tags "elastic gocdk sqlite ydb tikv"

test:
	cd weed; go test -tags "elastic gocdk sqlite ydb tikv" ./...

build: build-amd64 build-arm64

build-amd64:
	mkdir -p ./build/linux-amd64
	cd weed; GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o ../build/linux-amd64/weed

build-arm64:
	mkdir -p ./build/linux-arm64
	cd weed; GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o ../build/linux-arm64/weed
	mkdir -p ./build/darwin-arm64
	cd weed; GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o ../build/darwin-arm64/weed
