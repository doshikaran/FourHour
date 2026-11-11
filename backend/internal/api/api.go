// Package api is the HTTP layer. Every route is under /api (CONTRACTS.md
// section 1) and every error uses the contract's error shape.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ErrorBody is the contract error shape:
// { "error": { "code": "...", "message": "...", "paypal_debug_id": null } }
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the inner error object. PayPalDebugID is null unless a
// PayPal call caused the error.
type ErrorDetail struct {
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	PayPalDebugID *string `json:"paypal_debug_id"`
}

// HealthResponse is GET /api/health.
type HealthResponse struct {
	Status          string   `json:"status"`
	PayPalEnv       string   `json:"paypal_env"`
	ArtifactsLoaded []string `json:"artifacts_loaded"`
}

// Deps are the server's dependencies, injected at startup.
type Deps struct {
	DB              *sql.DB
	Log             *slog.Logger
	ArtifactsLoaded []string
}

// NewHandler returns the root handler.
func NewHandler(d Deps) http.Handler {
	loaded := append([]string{}, d.ArtifactsLoaded...) // never nil, never shared
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.PingContext(ctx); err != nil {
			d.Log.Error("health: database ping failed", "err", err)
			WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "The database is not reachable.", nil)
			return
		}
		WriteJSON(w, http.StatusOK, HealthResponse{Status: "ok", PayPalEnv: "sandbox", ArtifactsLoaded: loaded})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "No route "+r.Method+" "+r.URL.Path+".", nil)
	})
	return recoverer(d.Log, mux)
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":{"code":"internal_error","message":"response encoding failed","paypal_debug_id":null}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n')) // a failed write means the client went away; nothing to do
}

// WriteError writes the contract error shape.
func WriteError(w http.ResponseWriter, status int, code, message string, paypalDebugID *string) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message, PayPalDebugID: paypalDebugID}})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic in handler", "path", r.URL.Path, "panic", v)
				WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error.", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
