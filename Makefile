BIN ?= cxbrush

.PHONY: all build web deps test fmt vet run clean

all: build

deps:
	cd web && pnpm install

# 前端构建产物会 embed 进二进制，改完前端必须重新 build。
web:
	cd web && pnpm build

build: web
	go build -o $(BIN) ./cmd/cxbrush

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './web/*')

vet:
	go vet ./...

run: build
	./$(BIN) serve

clean:
	rm -f $(BIN)
