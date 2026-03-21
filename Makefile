.PHONY: build server cli test clean

build: server cli

server:
	go build -o bin/droply-server ./cmd/droply-server

cli:
	go build -o bin/droply ./cmd/droply

test:
	go test ./...

clean:
	rm -rf bin/
