.PHONY: build worker run run-all run-worker test clean

build:
	go build -o gateway.exe cmd/gateway/main.go

worker:
	go build -o worker.exe cmd/worker/main.go

run: build
	./gateway.exe

# run-all starts both the gateway and the worker: the gateway serves HTTP and
# replays pending buffers/outbox at startup, the worker consumes the jobs queue
# (L1-L4 refinement, wiki/codegraph/skill review, git batch commit) and runs
# the recording watchdog. Without the worker, refinement and compensation never
# run (ALL-82).
run-all: build worker
	./gateway.exe & ./worker.exe

run-worker: worker
	./worker.exe

test:
	go test -v ./...

test-coverage:
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	rm -f gateway.exe coverage.out coverage.html

deps:
	go mod download
	go mod tidy

migrate:
	go run cmd/gateway/main.go migrate

health:
	curl http://127.0.0.1:8096/health
