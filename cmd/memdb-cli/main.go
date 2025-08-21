// Command memdb-cli is a small command-line client for memdb-server.
//
//	memdb-cli [-addr host:port] [-timeout 5s] <command> [args]
//
// Commands:
//
//	create <table>                      create a table
//	drop <table>                        drop a table
//	tables                              list tables
//	put <table> <key> <value>           store a value
//	get <table> <key>                   fetch a value
//	del <table> <key>                   delete a key
//	scan <table> [start] [end] [limit]  stream keys in [start, end)
//	batch <table> k=v [k=v ...]         write several entries atomically
//	stats [table]                       show table statistics
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/akshaynavani/MemDB/api/memdbpb"
)

const usage = `usage: memdb-cli [-addr host:port] [-timeout d] <command> [args]

commands:
  create <table>
  drop <table>
  tables
  put <table> <key> <value>
  get <table> <key>
  del <table> <key>
  scan <table> [start] [end] [limit]   (use "" for an open bound)
  batch <table> k=v [k=v ...]
  stats [table]
`

func main() {
	addr := flag.String("addr", "localhost:7070", "server address")
	timeout := flag.Duration("timeout", 5*time.Second, "per-command timeout")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c := &cli{client: memdbpb.NewMemDBClient(conn), out: os.Stdout}
	if err := c.run(ctx, args[0], args[1:]); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(os.Stderr, ue)
			flag.Usage()
			os.Exit(2)
		}
		fatal(err)
	}
}

func fatal(err error) {
	if s, ok := status.FromError(err); ok {
		fmt.Fprintf(os.Stderr, "error: %s: %s\n", s.Code(), s.Message())
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(1)
}

type usageError string

func (e usageError) Error() string { return string(e) }

func wantArgs(cmd string, args []string, lo, hi int) error {
	if len(args) < lo || len(args) > hi {
		return usageError(fmt.Sprintf("%s: wrong number of arguments", cmd))
	}
	return nil
}

type cli struct {
	client memdbpb.MemDBClient
	out    io.Writer
}

func (c *cli) run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "create":
		if err := wantArgs(cmd, args, 1, 1); err != nil {
			return err
		}
		if _, err := c.client.CreateTable(ctx, &memdbpb.CreateTableRequest{Name: args[0]}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "created %s\n", args[0])

	case "drop":
		if err := wantArgs(cmd, args, 1, 1); err != nil {
			return err
		}
		if _, err := c.client.DropTable(ctx, &memdbpb.DropTableRequest{Name: args[0]}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "dropped %s\n", args[0])

	case "tables":
		if err := wantArgs(cmd, args, 0, 0); err != nil {
			return err
		}
		resp, err := c.client.ListTables(ctx, &memdbpb.ListTablesRequest{})
		if err != nil {
			return err
		}
		for _, n := range resp.GetNames() {
			fmt.Fprintln(c.out, n)
		}

	case "put":
		if err := wantArgs(cmd, args, 3, 3); err != nil {
			return err
		}
		resp, err := c.client.Put(ctx, &memdbpb.PutRequest{Table: args[0], Key: []byte(args[1]), Value: []byte(args[2])})
		if err != nil {
			return err
		}
		if resp.GetReplaced() {
			fmt.Fprintln(c.out, "OK (replaced)")
		} else {
			fmt.Fprintln(c.out, "OK")
		}

	case "get":
		if err := wantArgs(cmd, args, 2, 2); err != nil {
			return err
		}
		resp, err := c.client.Get(ctx, &memdbpb.GetRequest{Table: args[0], Key: []byte(args[1])})
		if err != nil {
			return err
		}
		if !resp.GetFound() {
			fmt.Fprintln(c.out, "(not found)")
			return nil
		}
		fmt.Fprintf(c.out, "%s\n", resp.GetValue())

	case "del":
		if err := wantArgs(cmd, args, 2, 2); err != nil {
			return err
		}
		resp, err := c.client.Delete(ctx, &memdbpb.DeleteRequest{Table: args[0], Key: []byte(args[1])})
		if err != nil {
			return err
		}
		if resp.GetDeleted() {
			fmt.Fprintln(c.out, "deleted")
		} else {
			fmt.Fprintln(c.out, "(not found)")
		}

	case "scan":
		return c.scan(ctx, args)

	case "batch":
		return c.batch(ctx, args)

	case "stats":
		return c.stats(ctx, args)

	default:
		return usageError(fmt.Sprintf("unknown command %q", cmd))
	}
	return nil
}

func (c *cli) scan(ctx context.Context, args []string) error {
	if err := wantArgs("scan", args, 1, 4); err != nil {
		return err
	}
	req := &memdbpb.ScanRequest{Table: args[0]}
	if len(args) > 1 {
		req.Start = []byte(args[1])
	}
	if len(args) > 2 {
		req.End = []byte(args[2])
	}
	if len(args) > 3 {
		n, err := strconv.ParseUint(args[3], 10, 32)
		if err != nil {
			return usageError(fmt.Sprintf("scan: invalid limit %q", args[3]))
		}
		req.Limit = uint32(n)
	}

	stream, err := c.client.Scan(ctx, req)
	if err != nil {
		return err
	}
	count := 0
	for {
		kv, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s\t%s\n", kv.GetKey(), kv.GetValue())
		count++
	}
	fmt.Fprintf(c.out, "(%d entries)\n", count)
	return nil
}

func (c *cli) batch(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return usageError("batch: need a table and at least one k=v pair")
	}
	req := &memdbpb.BatchPutRequest{Table: args[0]}
	for _, pair := range args[1:] {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return usageError(fmt.Sprintf("batch: %q is not k=v", pair))
		}
		req.Entries = append(req.Entries, &memdbpb.KeyValue{Key: []byte(k), Value: []byte(v)})
	}
	resp, err := c.client.BatchPut(ctx, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "OK (%d written, %d new)\n", len(req.Entries), resp.GetInserted())
	return nil
}

func (c *cli) stats(ctx context.Context, args []string) error {
	if err := wantArgs("stats", args, 0, 1); err != nil {
		return err
	}
	req := &memdbpb.StatsRequest{}
	if len(args) == 1 {
		req.Table = args[0]
	}
	resp, err := c.client.Stats(ctx, req)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TABLE\tKEYS\tHEIGHT\tORDER\tPUTS\tGETS\tHITS\tDELETES\tSCANS\tBATCHES")
	for _, t := range resp.GetTables() {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			t.GetName(), t.GetKeys(), t.GetHeight(), t.GetOrder(),
			t.GetPuts(), t.GetGets(), t.GetHits(), t.GetDeletes(), t.GetScans(), t.GetBatches())
	}
	return w.Flush()
}
