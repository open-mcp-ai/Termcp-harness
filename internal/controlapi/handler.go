package controlapi

import (
	"encoding/json"
	"net/http"

	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

type Snapshotter interface {
	Snapshot() runtime.Snapshot
}

type healthResponse struct {
	Status  string        `json:"status"`
	State   runtime.State `json:"state"`
	Version string        `json:"version"`
}

func NewHandler(source Snapshotter) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := source.Snapshot()
		statusCode := http.StatusOK
		status := "ok"
		if snapshot.State != runtime.StateRunning {
			statusCode = http.StatusServiceUnavailable
			status = "unavailable"
		}

		writeJSON(w, statusCode, healthResponse{
			Status:  status,
			State:   snapshot.State,
			Version: snapshot.Version,
		})
	})

	mux.HandleFunc("GET /v1/runtime", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, source.Snapshot())
	})

	return mux
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
