package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/akshaynavani/MemDB/api/memdbpb"
	"github.com/akshaynavani/MemDB/engine"
)

// startServer runs the service over an in-memory bufconn listener and
// returns a connected client.
func startServer(t *testing.T, opts ...Option) memdbpb.MemDBClient {
	t.Helper()
	eng, err := engine.New(engine.Options{Order: 4})
	if err != nil {
		t.Fatal(err)
	}
	lis := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	New(eng, opts...).Register(g)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return memdbpb.NewMemDBClient(conn)
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if got := status.Code(err); got != code {
		t.Fatalf("got code %s (%v), want %s", got, err, code)
	}
}

func scanAll(t *testing.T, c memdbpb.MemDBClient, req *memdbpb.ScanRequest) []string {
	t.Helper()
	stream, err := c.Scan(testCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for {
		kv, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return keys
		}
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, string(kv.GetKey()))
	}
}

func TestTableRPCs(t *testing.T) {
	c := startServer(t)
	ctx := testCtx(t)

	if _, err := c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	_, err := c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "a"})
	wantCode(t, err, codes.AlreadyExists)
	_, err = c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "bad name"})
	wantCode(t, err, codes.InvalidArgument)

	resp, err := c.ListTables(ctx, &memdbpb.ListTablesRequest{})
	if err != nil || len(resp.GetNames()) != 1 || resp.GetNames()[0] != "a" {
		t.Fatalf("ListTables = %v, %v", resp.GetNames(), err)
	}

	if _, err := c.DropTable(ctx, &memdbpb.DropTableRequest{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.DropTable(ctx, &memdbpb.DropTableRequest{Name: "a"})
	wantCode(t, err, codes.NotFound)
}

func TestKeyRPCs(t *testing.T) {
	c := startServer(t)
	ctx := testCtx(t)
	c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "kv"})

	_, err := c.Put(ctx, &memdbpb.PutRequest{Table: "missing", Key: []byte("k"), Value: []byte("v")})
	wantCode(t, err, codes.NotFound)
	_, err = c.Put(ctx, &memdbpb.PutRequest{Table: "kv", Value: []byte("v")})
	wantCode(t, err, codes.InvalidArgument)
	_, err = c.Get(ctx, &memdbpb.GetRequest{Table: "kv"})
	wantCode(t, err, codes.InvalidArgument)

	put, err := c.Put(ctx, &memdbpb.PutRequest{Table: "kv", Key: []byte("k"), Value: []byte("v1")})
	if err != nil || put.GetReplaced() {
		t.Fatalf("first put: %v %v", put, err)
	}
	put, err = c.Put(ctx, &memdbpb.PutRequest{Table: "kv", Key: []byte("k"), Value: []byte("v2")})
	if err != nil || !put.GetReplaced() {
		t.Fatalf("second put: %v %v", put, err)
	}

	get, err := c.Get(ctx, &memdbpb.GetRequest{Table: "kv", Key: []byte("k")})
	if err != nil || !get.GetFound() || string(get.GetValue()) != "v2" {
		t.Fatalf("get: %v %v", get, err)
	}

	del, err := c.Delete(ctx, &memdbpb.DeleteRequest{Table: "kv", Key: []byte("k")})
	if err != nil || !del.GetDeleted() {
		t.Fatalf("delete: %v %v", del, err)
	}
	get, err = c.Get(ctx, &memdbpb.GetRequest{Table: "kv", Key: []byte("k")})
	if err != nil || get.GetFound() {
		t.Fatalf("get after delete: %v %v", get, err)
	}
}

func TestBatchPutAndScan(t *testing.T) {
	// A tiny page size forces Scan through several pages.
	c := startServer(t, WithScanPageSize(7))
	ctx := testCtx(t)
	c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "kv"})

	req := &memdbpb.BatchPutRequest{Table: "kv"}
	for i := 0; i < 50; i++ {
		req.Entries = append(req.Entries, &memdbpb.KeyValue{
			Key:   []byte(fmt.Sprintf("k%03d", i)),
			Value: []byte(fmt.Sprintf("v%d", i)),
		})
	}
	resp, err := c.BatchPut(ctx, req)
	if err != nil || resp.GetInserted() != 50 {
		t.Fatalf("BatchPut: %v %v", resp, err)
	}

	bad := &memdbpb.BatchPutRequest{Table: "kv", Entries: []*memdbpb.KeyValue{
		{Key: []byte("new"), Value: []byte("x")},
		{Key: nil, Value: []byte("x")},
	}}
	_, err = c.BatchPut(ctx, bad)
	wantCode(t, err, codes.InvalidArgument)

	all := scanAll(t, c, &memdbpb.ScanRequest{Table: "kv"})
	if len(all) != 50 {
		t.Fatalf("full scan returned %d keys, want 50 (a rejected batch must not leak)", len(all))
	}
	for i, k := range all {
		if k != fmt.Sprintf("k%03d", i) {
			t.Fatalf("position %d = %s", i, k)
		}
	}

	ranged := scanAll(t, c, &memdbpb.ScanRequest{Table: "kv", Start: []byte("k010"), End: []byte("k030")})
	if len(ranged) != 20 || ranged[0] != "k010" || ranged[19] != "k029" {
		t.Fatalf("ranged scan = %v", ranged)
	}

	limited := scanAll(t, c, &memdbpb.ScanRequest{Table: "kv", Start: []byte("k005"), Limit: 16})
	if len(limited) != 16 || limited[0] != "k005" || limited[15] != "k020" {
		t.Fatalf("limited scan = %v", limited)
	}

	stream, err := c.Scan(ctx, &memdbpb.ScanRequest{Table: "missing"})
	if err == nil {
		_, err = stream.Recv()
	}
	wantCode(t, err, codes.NotFound)
}

func TestStatsRPC(t *testing.T) {
	c := startServer(t)
	ctx := testCtx(t)
	c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "a"})
	c.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: "b"})
	c.Put(ctx, &memdbpb.PutRequest{Table: "a", Key: []byte("k"), Value: []byte("v")})
	c.Get(ctx, &memdbpb.GetRequest{Table: "a", Key: []byte("k")})

	all, err := c.Stats(ctx, &memdbpb.StatsRequest{})
	if err != nil || len(all.GetTables()) != 2 {
		t.Fatalf("Stats(all) = %v, %v", all, err)
	}
	one, err := c.Stats(ctx, &memdbpb.StatsRequest{Table: "a"})
	if err != nil || len(one.GetTables()) != 1 {
		t.Fatalf("Stats(a) = %v, %v", one, err)
	}
	s := one.GetTables()[0]
	if s.GetName() != "a" || s.GetKeys() != 1 || s.GetPuts() != 1 || s.GetGets() != 1 || s.GetHits() != 1 || s.GetOrder() != 4 {
		t.Fatalf("unexpected stats %+v", s)
	}
	_, err = c.Stats(ctx, &memdbpb.StatsRequest{Table: "nope"})
	wantCode(t, err, codes.NotFound)
}
