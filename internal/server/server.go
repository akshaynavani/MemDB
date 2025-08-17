// Package server implements the MemDB gRPC service on top of package engine.
package server

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/akshaynavani/MemDB/api/memdbpb"
	"github.com/akshaynavani/MemDB/engine"
)

// DefaultScanPageSize is how many entries a Scan reads from the table per
// lock acquisition.
const DefaultScanPageSize = 256

// Server adapts an *engine.Engine to the generated MemDBServer interface.
type Server struct {
	memdbpb.UnimplementedMemDBServer

	eng          *engine.Engine
	scanPageSize int
}

// Option customises a Server.
type Option func(*Server)

// WithScanPageSize sets how many entries Scan copies out of the table per
// read-lock acquisition. Smaller pages hold the lock for less time; larger
// pages take it less often.
func WithScanPageSize(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.scanPageSize = n
		}
	}
}

// New returns a Server backed by eng.
func New(eng *engine.Engine, opts ...Option) *Server {
	s := &Server{eng: eng, scanPageSize: DefaultScanPageSize}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Register attaches the service to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	memdbpb.RegisterMemDBServer(g, s)
}

// toStatus converts engine errors into gRPC status errors with fitting codes.
func toStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, engine.ErrTableNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, engine.ErrTableExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, engine.ErrInvalidName),
		errors.Is(err, engine.ErrEmptyKey),
		errors.Is(err, engine.ErrKeyTooLarge),
		errors.Is(err, engine.ErrValueTooLarge):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func (s *Server) table(name string) (*engine.Table, error) {
	t, err := s.eng.Table(name)
	if err != nil {
		return nil, toStatus(err)
	}
	return t, nil
}

func requireKey(key []byte) error {
	if len(key) == 0 {
		return toStatus(engine.ErrEmptyKey)
	}
	return nil
}

// nilIfEmpty maps an empty protobuf bytes field to "unbounded".
func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (s *Server) CreateTable(_ context.Context, req *memdbpb.CreateTableRequest) (*memdbpb.CreateTableResponse, error) {
	if _, err := s.eng.CreateTable(req.GetName()); err != nil {
		return nil, toStatus(err)
	}
	return &memdbpb.CreateTableResponse{}, nil
}

func (s *Server) DropTable(_ context.Context, req *memdbpb.DropTableRequest) (*memdbpb.DropTableResponse, error) {
	if err := s.eng.DropTable(req.GetName()); err != nil {
		return nil, toStatus(err)
	}
	return &memdbpb.DropTableResponse{}, nil
}

func (s *Server) ListTables(context.Context, *memdbpb.ListTablesRequest) (*memdbpb.ListTablesResponse, error) {
	return &memdbpb.ListTablesResponse{Names: s.eng.ListTables()}, nil
}

func (s *Server) Put(_ context.Context, req *memdbpb.PutRequest) (*memdbpb.PutResponse, error) {
	t, err := s.table(req.GetTable())
	if err != nil {
		return nil, err
	}
	replaced, err := t.Put(req.GetKey(), req.GetValue())
	if err != nil {
		return nil, toStatus(err)
	}
	return &memdbpb.PutResponse{Replaced: replaced}, nil
}

func (s *Server) Get(_ context.Context, req *memdbpb.GetRequest) (*memdbpb.GetResponse, error) {
	if err := requireKey(req.GetKey()); err != nil {
		return nil, err
	}
	t, err := s.table(req.GetTable())
	if err != nil {
		return nil, err
	}
	v, ok := t.Get(req.GetKey())
	return &memdbpb.GetResponse{Found: ok, Value: v}, nil
}

func (s *Server) Delete(_ context.Context, req *memdbpb.DeleteRequest) (*memdbpb.DeleteResponse, error) {
	if err := requireKey(req.GetKey()); err != nil {
		return nil, err
	}
	t, err := s.table(req.GetTable())
	if err != nil {
		return nil, err
	}
	return &memdbpb.DeleteResponse{Deleted: t.Delete(req.GetKey())}, nil
}

func (s *Server) BatchPut(_ context.Context, req *memdbpb.BatchPutRequest) (*memdbpb.BatchPutResponse, error) {
	t, err := s.table(req.GetTable())
	if err != nil {
		return nil, err
	}
	entries := make([]engine.Entry, len(req.GetEntries()))
	for i, kv := range req.GetEntries() {
		entries[i] = engine.Entry{Key: kv.GetKey(), Value: kv.GetValue()}
	}
	inserted, err := t.BatchPut(entries)
	if err != nil {
		return nil, toStatus(err)
	}
	return &memdbpb.BatchPutResponse{Inserted: uint32(inserted)}, nil
}

// Scan streams a key range page by page. Each page is copied out of the
// table under its read lock and the lock is released before anything is sent,
// so a slow client never blocks writers.
func (s *Server) Scan(req *memdbpb.ScanRequest, stream memdbpb.MemDB_ScanServer) error {
	t, err := s.table(req.GetTable())
	if err != nil {
		return err
	}
	ctx := stream.Context()
	start, end := nilIfEmpty(req.GetStart()), nilIfEmpty(req.GetEnd())
	remaining := int(req.GetLimit()) // 0 = unlimited

	for {
		if err := ctx.Err(); err != nil {
			return toStatus(err)
		}
		page := s.scanPageSize
		if remaining > 0 && remaining < page {
			page = remaining
		}
		entries, next := t.ScanPage(start, end, page)
		for _, e := range entries {
			if err := stream.Send(&memdbpb.KeyValue{Key: e.Key, Value: e.Value}); err != nil {
				return err
			}
		}
		if remaining > 0 {
			remaining -= len(entries)
			if remaining == 0 {
				return nil
			}
		}
		if next == nil {
			return nil
		}
		start = next
	}
}

func (s *Server) Stats(_ context.Context, req *memdbpb.StatsRequest) (*memdbpb.StatsResponse, error) {
	var stats []engine.TableStats
	if name := req.GetTable(); name != "" {
		t, err := s.table(name)
		if err != nil {
			return nil, err
		}
		stats = []engine.TableStats{t.Stats()}
	} else {
		stats = s.eng.Stats()
	}

	resp := &memdbpb.StatsResponse{Tables: make([]*memdbpb.TableStats, 0, len(stats))}
	for _, st := range stats {
		resp.Tables = append(resp.Tables, &memdbpb.TableStats{
			Name:    st.Name,
			Keys:    uint64(st.Keys),
			Height:  uint32(st.Height),
			Order:   uint32(st.Order),
			Puts:    st.Puts,
			Gets:    st.Gets,
			Hits:    st.Hits,
			Deletes: st.Deletes,
			Scans:   st.Scans,
			Batches: st.Batches,
		})
	}
	return resp, nil
}
