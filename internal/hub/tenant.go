package hub

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// database is the common query surface implemented by pgxpool.Pool and pgx.Tx.
// Authenticated requests receive a transaction-scoped implementation so every
// statement is protected by the same PostgreSQL tenant context.
type database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type tenantTransactionKey struct{}

func (s *Server) db(ctx context.Context) database {
	if tx, ok := ctx.Value(tenantTransactionKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.config.Pool
}

// begin starts a savepoint when a handler is already inside its tenant
// transaction. Public callbacks and collector enrollment still get a normal
// transaction from the pool.
func (s *Server) begin(ctx context.Context) (pgx.Tx, error) {
	if tx, ok := ctx.Value(tenantTransactionKey{}).(pgx.Tx); ok {
		return tx.Begin(ctx)
	}
	return s.config.WorkerPool.Begin(ctx)
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(value)
}

func (s *Server) tenantTransaction(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := principalFrom(request.Context())
		if !ok || principal.OrganizationID == "" {
			writeError(writer, http.StatusUnauthorized, "authentication_required", "An active workspace is required")
			return
		}
		tx, err := s.config.Pool.Begin(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "database_unavailable", "The workspace could not be opened")
			return
		}
		defer tx.Rollback(request.Context())
		if _, err = tx.Exec(request.Context(), `SELECT set_config('lens.organization_id',$1,true)`, principal.OrganizationID); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "database_unavailable", "The workspace could not be opened")
			return
		}
		response := &statusResponseWriter{ResponseWriter: writer}
		ctx := context.WithValue(request.Context(), tenantTransactionKey{}, tx)
		next.ServeHTTP(response, request.WithContext(ctx))
		if response.status >= http.StatusBadRequest {
			return
		}
		if err := tx.Commit(request.Context()); err != nil && response.status == 0 {
			writeError(writer, http.StatusServiceUnavailable, "database_unavailable", "The request could not be committed")
		}
	})
}
