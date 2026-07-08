.PHONY: build
build:
	go build -o dist/tfstateref-lint ./cmd/tfstateref-lint

.PHONY: install
install:
	go install github.com/takaishi/tfstateref-lint/cmd/tfstateref-lint

.PHONY: test
test:
	go test -race ./...
