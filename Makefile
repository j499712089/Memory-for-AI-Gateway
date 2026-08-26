.PHONY: build worker run run-all run-worker test test-coverage clean deps migrate health docs-assets

# docs-assets refreshes the vendored Swagger UI assets under docs/swagger-ui/
# (pinned swagger-ui-dist@5.19.0). The OpenAPI spec and these assets are
# embedded into the binary via go:embed, so the docs directory is never
# required at runtime and no external CDN is used (ALL-272).
docs-assets:
	npm pack swagger-ui-dist@5.19.0
	tar -xzf swagger-ui-dist-5.19.0.tgz -C docs/swagger-ui --strip-components=1 \
		package/swagger-ui-bundle.js package/swagger-ui.css \
		package/swagger-ui-standalone-preset.js \
		package/swagger-ui-bundle.js.map package/swagger-ui.css.map \
		package/favicon-16x16.png package/favicon-32x32.png
	rm -f swagger-ui-dist-5.19.0.tgz

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
