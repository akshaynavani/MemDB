# Build stage: generate the protobuf code, resolve modules and compile.
FROM golang:1.22-bookworm AS build

RUN apt-get update \
 && apt-get install -y --no-install-recommends protobuf-compiler make \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .
RUN make tools proto deps \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memdb-server ./cmd/memdb-server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memdb-cli ./cmd/memdb-cli

# Runtime stage: just the binaries.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/memdb-server /out/memdb-cli /usr/local/bin/
EXPOSE 7070
ENTRYPOINT ["/usr/local/bin/memdb-server"]
CMD ["-addr", ":7070"]
