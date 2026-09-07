package hub

import (
	"bytes"
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
type transactionOutcomeKey struct{}

type transactionOutcome struct {
	commitErrorResponse bool
}

// commitOnError records that the handler intentionally persisted a safe state
// transition while returning an error response (for example auth_error after a
// provider rejected setup). All other 4xx/5xx responses roll the request back.
func commitOnError(ctx context.Context) {
	if outcome, ok := ctx.Value(transactionOutcomeKey{}).(*transactionOutcome); ok {
		outcome.commitErrorResponse = true
	}
}

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

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

func (w *bufferedResponseWriter) flush(destination http.ResponseWriter) {
	for key, values := range w.header {
		destination.Header()[key] = append([]string(nil), values...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = destination.Write(w.body.Bytes())
}

func isMutation(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
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
		ctx := context.WithValue(request.Context(), tenantTransactionKey{}, tx)
		if !isMutation(request.Method) {
			response := &statusResponseWriter{ResponseWriter: writer}
			next.ServeHTTP(response, request.WithContext(ctx))
			if response.status < http.StatusBadRequest {
				_ = tx.Commit(request.Context())
			}
			return
		}
		outcome := &transactionOutcome{}
		ctx = context.WithValue(ctx, transactionOutcomeKey{}, outcome)
		response := newBufferedResponseWriter()
		next.ServeHTTP(response, request.WithContext(ctx))
		if response.status >= http.StatusBadRequest && !outcome.commitErrorResponse {
			response.flush(writer)
			return
		}
		if err := tx.Commit(request.Context()); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "database_unavailable", "The request could not be committed")
			return
		}
		response.flush(writer)
	})
}
