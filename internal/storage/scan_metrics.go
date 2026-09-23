package storage

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

type ScanWorkMetrics struct {
	statements   atomic.Int64
	rowsAffected atomic.Int64
}

func NewScanWorkMetrics() *ScanWorkMetrics { return &ScanWorkMetrics{} }

func (m *ScanWorkMetrics) Snapshot() (statements, rowsAffected int64) {
	if m == nil {
		return 0, 0
	}
	return m.statements.Load(), m.rowsAffected.Load()
}

type scanWorkContextKey struct{}

func ContextWithScanWork(ctx context.Context, metrics *ScanWorkMetrics) context.Context {
	return context.WithValue(ctx, scanWorkContextKey{}, metrics)
}

// QueryWorkTracer is inert unless a scan has put ScanWorkMetrics in its context.
func QueryWorkTracer() pgx.QueryTracer { return scanWorkTracer{} }

type scanWorkTracer struct{}

func (scanWorkTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (scanWorkTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	metrics, ok := ctx.Value(scanWorkContextKey{}).(*ScanWorkMetrics)
	if !ok || metrics == nil {
		return
	}
	metrics.statements.Add(1)
	if data.Err == nil {
		command := data.CommandTag.String()
		if strings.HasPrefix(command, "INSERT") || strings.HasPrefix(command, "UPDATE") || strings.HasPrefix(command, "DELETE") || strings.HasPrefix(command, "MERGE") {
			metrics.rowsAffected.Add(data.CommandTag.RowsAffected())
		}
	}
}
