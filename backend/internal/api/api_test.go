package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"fourhour/backend/internal/db"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testHandler(t *testing.T, loaded []string) (http.Handler, func()) {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(Deps{DB: d, Log: quiet(), ArtifactsLoaded: loaded}), func() { d.Close() }
}

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthShape(t *testing.T) {
	h, done := testHandler(t, []string{"pricing_table", "trigger_params"})
	defer done()
	rec := do(h, "GET", "/api/health")
	want := `{"status":"ok","paypal_env":"sandbox","artifacts_loaded":["pricing_table","trigger_params"]}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestHealthEmptyArtifactsIsArray(t *testing.T) {
	h, done := testHandler(t, nil)
	defer done()
	if got := do(h, "GET", "/api/health").Body.String(); got != `{"status":"ok","paypal_env":"sandbox","artifacts_loaded":[]}`+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	h, done := testHandler(t, nil)
	done() // close the DB
	rec := do(h, "GET", "/api/health")
	if rec.Code != 503 || rec.Body.String() != `{"error":{"code":"database_unavailable","message":"The database is not reachable.","paypal_debug_id":null}}`+"\n" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownRouteUsesErrorShape(t *testing.T) {
	h, done := testHandler(t, nil)
	defer done()
	rec := do(h, "GET", "/api/nope")
	if rec.Code != 404 || rec.Body.String() != `{"error":{"code":"not_found","message":"No route GET /api/nope.","paypal_debug_id":null}}`+"\n" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestRecovererUsesErrorShape(t *testing.T) {
	h := recoverer(quiet(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := do(h, "GET", "/api/x")
	if rec.Code != 500 || rec.Body.String() != `{"error":{"code":"internal_error","message":"Internal error.","paypal_debug_id":null}}`+"\n" {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
