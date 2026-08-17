.PHONY: build run test clean

build:
	go build -o gateway.exe cmd/gateway/main.go

run: build
	./gateway.exe

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
