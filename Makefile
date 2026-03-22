.PHONY: build server cli test clean deploy

build: server cli

server:
	go build -o bin/droply-server ./cmd/droply-server

cli:
	go build -o bin/droply ./cmd/droply

test:
	go test ./...

clean:
	rm -rf bin/

deploy:
	git pull
	go build -o bin/droply-server ./cmd/droply-server
	sudo systemctl stop droply
	sudo cp bin/droply-server /usr/local/bin/droply-server
	sudo systemctl start droply
