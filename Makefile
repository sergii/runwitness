.PHONY: build test race vet fmt-check acceptance cross-build ci clean

build:
	mkdir -p bin
	go build -o bin/runwitness ./cmd/runwitness

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	test -z "$$(gofmt -l .)"

acceptance: build
	python3 -m pip install -r tests/acceptance/requirements.txt
	python3 -m unittest discover -s tests/acceptance -p 'test_*.py' -v

cross-build:
	mkdir -p bin/cross
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/cross/runwitness-linux-amd64 ./cmd/runwitness
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/cross/runwitness-linux-arm64 ./cmd/runwitness
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/cross/runwitness-darwin-amd64 ./cmd/runwitness
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/cross/runwitness-darwin-arm64 ./cmd/runwitness
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/cross/runwitness-windows-amd64.exe ./cmd/runwitness

ci: fmt-check vet test race acceptance cross-build

clean:
	rm -rf bin
