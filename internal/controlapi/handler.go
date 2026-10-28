package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
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

type EventReader interface {
	Query(after eventlog.Sequence, limit int) (eventlog.Window, error)
}

type handlerOptions struct {
	events EventReader
	mcp    MCPLister
}

type Option func(*handlerOptions)

func WithEventLog(events EventReader) Option {
	return func(options *handlerOptions) {
		options.events = events
	}
}

type MCPLister interface {
	List() []mcpcontrol.Snapshot
}

func WithMCPRegistry(registry MCPLister) Option {
	return func(options *handlerOptions) {
		options.mcp = registry
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

type mcpServersResponse struct {
	Servers        []mcpcontrol.Snapshot `json:"servers"`
	Total          int                   `json:"total"`
	Connected      int                   `json:"connected"`
	Ready          int                   `json:"ready"`
	ControlPlaneOn bool                  `json:"control_plane_configured"`
}

func NewHandler(source Snapshotter, options ...Option) http.Handler {
	config := handlerOptions{}
	for _, option := range options {
		option(&config)
	}

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
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, request *http.Request) {
		if config.events == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "event log is not configured"})
			return
		}

		after, err := parseAfter(request.URL.Query().Get("after"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid after cursor"})
			return
		}
		limit, err := parseLimit(request.URL.Query().Get("limit"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		window, err := config.events.Query(after, limit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "event query failed"})
			return
		}
		writeJSON(w, http.StatusOK, window)
	})
	mux.HandleFunc("GET /v1/mcp/servers", func(w http.ResponseWriter, _ *http.Request) {
		response := mcpServersResponse{Servers: []mcpcontrol.Snapshot{}, ControlPlaneOn: config.mcp != nil}
		if config.mcp != nil {
			response.Servers = config.mcp.List()
			if response.Servers == nil {
				response.Servers = []mcpcontrol.Snapshot{}
			}
		}
		for _, server := range response.Servers {
			response.Total++
			if server.Connection == mcpcontrol.ConnectionConnected {
				response.Connected++
			}
			if server.Ready {
				response.Ready++
			}
		}
		writeJSON(w, http.StatusOK, response)
	})

	return mux
}

func parseAfter(raw string) (eventlog.Sequence, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return eventlog.Sequence(value), nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 100, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
