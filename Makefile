MODULE      := github.com/akshaynavani/MemDB
PROTO_DIR   := proto
PB_DIR      := api/memdbpb
BIN_DIR     := bin

PROTOC_GEN_GO_VERSION      := v1.35.1
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

.PHONY: all tools proto deps build test race vet clean

all: proto deps build

## tools: install the protoc plugins used by `make proto`
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

## proto: generate Go code for the gRPC API into api/memdbpb (not committed)
proto:
	mkdir -p $(PB_DIR)
	protoc -I $(PROTO_DIR) \
		--go_out=. --go_opt=module=$(MODULE) \
		--go-grpc_out=. --go-grpc_opt=module=$(MODULE) \
		$(PROTO_DIR)/memdb.proto

## deps: resolve module dependencies and write go.sum
deps:
	go mod tidy

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/memdb-server ./cmd/memdb-server
	go build -o $(BIN_DIR)/memdb-cli ./cmd/memdb-cli

test:
	go test ./...

race:
	go test -race -count=1 ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR) $(PB_DIR)
