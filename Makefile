.PHONY: build test acceptance ci clean

build:
	mkdir -p bin
	go build -o bin/runwitness ./cmd/runwitness

test:
	go test ./...

acceptance: build
	python3 -m pip install -r tests/acceptance/requirements.txt
	python3 tests/acceptance/test_runner_v001.py

ci: test acceptance

clean:
	rm -rf bin
