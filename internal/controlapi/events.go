package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

const (
	eventStreamBatchSize = 200
	eventStreamHeartbeat = 15 * time.Second
)

type eventWaiter interface {
	Wait(context.Context) error
}

func eventStreamHandler(events EventReader) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		if events == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "event log is not configured"})
			return
		}
		waiter, ok := events.(eventWaiter)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "event streaming is unavailable"})
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "event streaming is unsupported"})
			return
		}

		afterRaw := request.URL.Query().Get("after")
		if afterRaw == "" {
			afterRaw = request.Header.Get("Last-Event-ID")
		}
		after, err := parseAfter(afterRaw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid after cursor"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
			return
		}
		flusher.Flush()

		cursor := after
		emitEmpty := true
		for {
			sent, next, err := writeEventWindow(w, events, cursor, emitEmpty, flusher)
			if err != nil {
				return
			}
			emitEmpty = false
			if sent {
				cursor = next
				continue
			}

			waitContext, cancel := context.WithTimeout(request.Context(), eventStreamHeartbeat)
			err = waiter.Wait(waitContext)
			cancel()
			if errors.Is(err, context.DeadlineExceeded) {
				if _, writeErr := fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339)); writeErr != nil {
					return
				}
				flusher.Flush()
				continue
			}
			if err != nil {
				return
			}
		}
	}
}

func writeEventWindow(
	w http.ResponseWriter,
	events EventReader,
	after eventlog.Sequence,
	emitEmpty bool,
	flusher http.Flusher,
) (bool, eventlog.Sequence, error) {
	window, err := events.Query(after, eventStreamBatchSize)
	if err != nil {
		return false, after, err
	}
	if len(window.Events) == 0 {
		if emitEmpty || window.LatestSequence > after || window.Truncated || window.ControlOverflow {
			payload, err := json.Marshal(window)
			if err != nil {
				return false, after, err
			}
			if _, err := fmt.Fprintf(w, "event: events\ndata: %s\n\n", payload); err != nil {
				return false, after, err
			}
			flusher.Flush()
		}
		return false, after, nil
	}

	payload, err := json.Marshal(window)
	if err != nil {
		return false, after, err
	}
	next := window.Events[len(window.Events)-1].Sequence
	if _, err := fmt.Fprintf(
		w,
		"id: %s\nevent: events\ndata: %s\n\n",
		strconv.FormatUint(uint64(next), 10),
		payload,
	); err != nil {
		return false, after, err
	}
	flusher.Flush()
	return true, next, nil
}
