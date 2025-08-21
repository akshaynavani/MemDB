package server

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor recovers from panics in handlers, turning them into
// codes.Internal, and logs each call's method, duration and status code at
// debug level. Server-side failures (anything other than OK or an expected
// client error such as NotFound) are logged at warn.
func UnaryInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		start := time.Now()
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in handler", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
			logCall(log, info.FullMethod, start, err)
		}()
		return handler(ctx, req)
	}
}

// StreamInterceptor is the streaming counterpart of UnaryInterceptor.
func StreamInterceptor(log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		start := time.Now()
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic in stream handler", "method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
			logCall(log, info.FullMethod, start, err)
		}()
		return handler(srv, ss)
	}
}

func logCall(log *slog.Logger, method string, start time.Time, err error) {
	code := status.Code(err)
	attrs := []any{"method", method, "code", code.String(), "duration", time.Since(start)}
	switch code {
	case codes.OK, codes.NotFound, codes.AlreadyExists, codes.InvalidArgument, codes.Canceled:
		log.Debug("rpc", attrs...)
	default:
		log.Warn("rpc", append(attrs, "error", err)...)
	}
}
