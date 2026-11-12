BINARY := qwen-sub-proxy

.PHONY: build test vet smoke healthcheck install uninstall clean

build:
	go build -o $(BINARY) ./cmd/qwen-sub-proxy

test:
	go test ./...

vet:
	go vet ./...

smoke: build
	./scripts/smoke.sh

healthcheck: build
	./$(BINARY) healthcheck

install: build
	./scripts/install.sh

uninstall:
	./scripts/uninstall.sh

clean:
	rm -f $(BINARY)
