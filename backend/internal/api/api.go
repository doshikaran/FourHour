// Package api is the HTTP layer. Every route is under /api (CONTRACTS.md
// section 1) and every error uses the contract's error shape.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fourhour/backend/internal/artifacts"
	"fourhour/backend/internal/model"
	"fourhour/backend/internal/replay"
	"fourhour/backend/internal/service"
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
	DB      *sql.DB
	Log     *slog.Logger
	Service *service.Service // nil only in the health-only tests
	Engine  *replay.Engine
	// ArtifactsDir is re-read on each request, so artifacts written by ml/
	// while the server runs are picked up without a restart.
	ArtifactsDir string
	// ArtifactsLoaded overrides the health listing (tests).
	ArtifactsLoaded func() []string
	FrontendURL     string // CORS origin
}

const maxBodyBytes = 1 << 20

// NewHandler returns the root handler.
func NewHandler(d Deps) http.Handler {
	h := &handlers{d: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "No route "+r.Method+" "+r.URL.Path+".", nil)
	})
	if d.Service != nil {
		mux.HandleFunc("GET /api/counties", h.counties)
		mux.HandleFunc("POST /api/quotes", h.quote)
		mux.HandleFunc("POST /api/members", h.createMember)
		mux.HandleFunc("POST /api/policies", h.createPolicy)
		mux.HandleFunc("GET /api/policies", h.listPolicies)
		mux.HandleFunc("GET /api/policies/{id}", h.getPolicy)
		mux.HandleFunc("POST /api/policies/{id}/charge-premium", h.chargePremium)
		mux.HandleFunc("POST /api/policies/{id}/cancel", h.cancelPolicy)
		mux.HandleFunc("GET /api/paypal/vault/return", h.vaultReturn)
		mux.HandleFunc("GET /api/paypal/vault/cancel", h.vaultCancel)
		mux.HandleFunc("POST /api/paypal/webhook", h.webhook)
		mux.HandleFunc("POST /api/witness/devices", h.registerDevice)
		mux.HandleFunc("POST /api/witness/entries", h.addEntries)
		mux.HandleFunc("GET /api/witness/devices/{id}/entries", h.listEntries)
		mux.HandleFunc("GET /api/claims", h.listClaims)
		mux.HandleFunc("GET /api/claims/{id}", h.getClaim)
		mux.HandleFunc("GET /api/ledger", h.ledger)
		for path, name := range map[string]string{
			"backtest": artifacts.Backtest, "pricing": artifacts.PricingTable, "solvency": artifacts.Solvency,
			"basis-risk": artifacts.BasisRisk, "trigger": artifacts.TriggerParams, "data-quality": artifacts.DataQuality,
		} {
			mux.HandleFunc("GET /api/analytics/"+path, h.analytics(name))
		}
	}
	if d.Engine != nil {
		mux.HandleFunc("GET /api/replay/scenarios", h.scenarios)
		mux.HandleFunc("POST /api/replay/start", h.replayStart)
		mux.HandleFunc("POST /api/replay/stop", h.replayStop)
		mux.HandleFunc("POST /api/replay/reset", h.replayReset)
		mux.HandleFunc("GET /api/replay/stream", h.stream)
	}
	return recoverer(d.Log, cors(d.FrontendURL, mux))
}

type handlers struct{ d Deps }

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

// fail maps an error to the contract shape. Unknown errors are logged and
// reported as internal_error without their text.
func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *service.Error
	if errors.As(err, &se) {
		if se.Status >= 500 {
			h.d.Log.Error("request failed", "path", r.URL.Path, "code", se.Code, "err", err)
		}
		WriteError(w, se.Status, se.Code, se.Message, se.DebugID)
		return
	}
	h.d.Log.Error("request failed", "path", r.URL.Path, "err", err)
	WriteError(w, http.StatusInternalServerError, "internal_error", "Internal error.", nil)
}

// decode reads a JSON body strictly: one object, known fields only.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", "Request body is not valid JSON for this endpoint: "+err.Error(), nil)
		return false
	}
	if dec.More() {
		WriteError(w, http.StatusBadRequest, "invalid_json", "Request body has trailing data.", nil)
		return false
	}
	return true
}

func cors(origin string, next http.Handler) http.Handler {
	origin = strings.TrimRight(origin, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin != "" && r.Header.Get("Origin") == origin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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

// ---------------------------------------------------------------------------
// Reference data, quotes, members, policies

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.d.DB.PingContext(ctx); err != nil {
		h.d.Log.Error("health: database ping failed", "err", err)
		WriteError(w, http.StatusServiceUnavailable, "database_unavailable", "The database is not reachable.", nil)
		return
	}
	var loaded []string
	if h.d.ArtifactsLoaded != nil {
		loaded = append([]string{}, h.d.ArtifactsLoaded()...)
	} else {
		loaded = artifacts.Load(h.d.ArtifactsDir, slog.New(slog.NewTextHandler(io.Discard, nil))).LoadedNames()
	}
	WriteJSON(w, http.StatusOK, HealthResponse{Status: "ok", PayPalEnv: "sandbox", ArtifactsLoaded: loaded})
}

// County is one GET /api/counties item. DataCoverage is null until
// data_quality.json has a value for the county.
type County struct {
	FIPS         string   `json:"fips"`
	Name         string   `json:"name"`
	State        string   `json:"state"`
	DataCoverage *float64 `json:"data_coverage"`
}

// demoCounties are the three counties of SPEC.md section 2.
var demoCounties = []County{
	{FIPS: "48201", Name: "Harris County", State: "TX"},
	{FIPS: "12086", Name: "Miami-Dade County", State: "FL"},
	{FIPS: "17031", Name: "Cook County", State: "IL"},
}

func (h *handlers) counties(w http.ResponseWriter, r *http.Request) {
	cov := artifacts.CountyCoverage(h.d.ArtifactsDir)
	out := make([]County, len(demoCounties))
	for i, c := range demoCounties {
		c.DataCoverage = cov[c.FIPS]
		out[i] = c
	}
	WriteJSON(w, http.StatusOK, map[string][]County{"counties": out})
}

type quoteRequest struct {
	FIPS       string `json:"fips"`
	CoverCents int64  `json:"cover_cents"`
}

func (h *handlers) quote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if !decode(w, r, &req) {
		return
	}
	q, err := h.d.Service.Quote(r.Context(), req.FIPS, req.CoverCents)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, q)
}

type memberRequest struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	PayPalEmail string `json:"paypal_email"`
}

func (h *handlers) createMember(w http.ResponseWriter, r *http.Request) {
	var req memberRequest
	if !decode(w, r, &req) {
		return
	}
	m, err := h.d.Service.CreateMember(r.Context(), req.Name, req.Email, req.PayPalEmail)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, m)
}

type policyRequest struct {
	MemberID   string `json:"member_id"`
	FIPS       string `json:"fips"`
	CoverCents int64  `json:"cover_cents"`
}

func (h *handlers) createPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if !decode(w, r, &req) {
		return
	}
	p, err := h.d.Service.CreatePolicy(r.Context(), req.MemberID, req.FIPS, req.CoverCents)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, p)
}

func (h *handlers) listPolicies(w http.ResponseWriter, r *http.Request) {
	ps, err := h.d.Service.ListPolicies(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string][]model.Policy{"policies": ps})
}

func (h *handlers) getPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.d.Service.GetPolicyDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, p)
}

type chargeRequest struct {
	Period string `json:"period"`
}

func (h *handlers) chargePremium(w http.ResponseWriter, r *http.Request) {
	var req chargeRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := h.d.Service.ChargePremium(r.Context(), r.PathValue("id"), req.Period)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) cancelPolicy(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusNotImplemented, "not_implemented", "Cancel with pro-rata refund is a stretch item (D9) and is not built yet.", nil)
}

func (h *handlers) vaultReturn(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	http.Redirect(w, r, h.d.Service.VaultReturn(ctx, r.URL.Query().Get("policy_id")), http.StatusFound)
}

func (h *handlers) vaultCancel(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, strings.TrimRight(h.d.FrontendURL, "/")+"/enrol?cancelled=1", http.StatusFound)
}

func (h *handlers) webhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_webhook", "Body could not be read.", nil)
		return
	}
	if err := h.d.Service.HandleWebhook(r.Context(), r.Header, raw); err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// ---------------------------------------------------------------------------
// Witness

type deviceRequest struct {
	PolicyID     string          `json:"policy_id"`
	PublicKeyJWK json.RawMessage `json:"public_key_jwk"`
}

func (h *handlers) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decode(w, r, &req) {
		return
	}
	d, err := h.d.Service.RegisterDevice(r.Context(), req.PolicyID, req.PublicKeyJWK)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, d)
}

type entriesRequest struct {
	DeviceID string            `json:"device_id"`
	Entries  []json.RawMessage `json:"entries"`
}

func (h *handlers) addEntries(w http.ResponseWriter, r *http.Request) {
	// Entries are parsed one by one so a malformed entry is reported as
	// "malformed" without failing the whole upload.
	var req entriesRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil || req.DeviceID == "" {
		WriteError(w, http.StatusBadRequest, "invalid_json", "Body must be {\"device_id\": \"dev_...\", \"entries\": [...]}.", nil)
		return
	}
	res, err := h.d.Service.AddEntries(r.Context(), req.DeviceID, req.Entries)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) listEntries(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 1000 {
			WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be 1 to 1000.", nil)
			return
		}
		limit = n
	}
	es, err := h.d.Service.ListEntries(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string][]model.WitnessEntry{"entries": es})
}

// ---------------------------------------------------------------------------
// Claims, ledger, analytics

func (h *handlers) listClaims(w http.ResponseWriter, r *http.Request) {
	cs, err := h.d.Service.ClaimItems(r.Context(), nil)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string][]model.ClaimItem{"claims": cs})
}

func (h *handlers) getClaim(w http.ResponseWriter, r *http.Request) {
	c, err := h.d.Service.GetClaimDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) ledger(w http.ResponseWriter, r *http.Request) {
	l, err := h.d.Service.Ledger(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, l)
}

// analytics serves an artifact unchanged (CONTRACTS.md section 4).
func (h *handlers) analytics(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := artifacts.ReadRaw(h.d.ArtifactsDir, name)
		if errors.Is(err, artifacts.ErrMissing) {
			WriteError(w, http.StatusNotFound, "artifact_missing", name+".json is not available.", nil)
			return
		}
		if err != nil {
			h.d.Log.Warn("artifact invalid", "name", name, "err", err)
			WriteError(w, http.StatusServiceUnavailable, "artifact_invalid", name+".json failed validation: "+err.Error(), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.TrimSpace(b))
	}
}

// ---------------------------------------------------------------------------
// Replay

func (h *handlers) scenarios(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string][]replay.ScenarioItem{"scenarios": h.d.Engine.Scenarios()})
}

type startRequest struct {
	ScenarioID string  `json:"scenario_id"`
	Speed      float64 `json:"speed"`
}

func (h *handlers) replayStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.d.Engine.Start(req.ScenarioID, req.Speed)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) replayStop(w http.ResponseWriter, r *http.Request) {
	h.d.Engine.Stop()
	WriteJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

func (h *handlers) replayReset(w http.ResponseWriter, r *http.Request) {
	h.d.Engine.Reset()
	WriteJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

// stream serves Server-Sent Events: a snapshot first, then live messages,
// with a comment line every 15 seconds to keep proxies from closing it.
func (h *handlers) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	sub, snap, err := h.d.Engine.Subscribe(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer h.d.Service.Hub().Unsubscribe(sub)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	b, err := json.Marshal(snap)
	if err != nil {
		h.d.Log.Error("stream: snapshot", "err", err)
		return
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", model.EvSnapshot, b); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keep.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
		case m, ok := <-sub.C:
			if !ok {
				return // dropped as too slow; the client reconnects and gets a snapshot
			}
			if _, err := w.Write(m.Format()); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
