package mcpcontrol

import (
	"context"
	"time"
)

func (h *Host) SetDesiredEnabled(id string, enabled bool) error {
	managed, err := h.managed(id)
	if err != nil {
		return err
	}
	managed.mu.Lock()
	managed.desiredEnabled = enabled
	if enabled {
		managed.desiredConnected = true
		managed.autoReconnect = true
		managed.nextAttempt = time.Time{}
	}
	managed.mu.Unlock()
	return nil
}

// Run starts supervised MCP lifecycle maintenance and blocks until ctx ends.
// Unexpected connection loss is reconnected only when the desired state is
// enabled and automatic reconnects have not been explicitly disabled.
func (h *Host) Run(ctx context.Context) {
	if h == nil {
		return
	}
	supervisionContext, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	if h.runCancel != nil {
		h.runCancel()
	}
	h.runCancel = cancel
	h.runContext = supervisionContext
	for id, managed := range h.servers {
		h.startSupervisorLocked(id, managed)
	}
	h.mu.Unlock()
	<-supervisionContext.Done()
}

func (h *Host) startSupervisorLocked(id string, managed *managedServer) {
	if h.runContext == nil || managed.supervising {
		return
	}
	managed.supervising = true
	go h.supervise(h.runContext, id, managed)
}

func (h *Host) supervise(ctx context.Context, id string, managed *managedServer) {
	interval := h.supervisionInterval()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			managed.mu.Lock()
			managed.supervising = false
			managed.mu.Unlock()
			return
		case <-timer.C:
			h.maintain(ctx, id, managed)
			timer.Reset(interval)
		}
	}
}

func (h *Host) maintain(ctx context.Context, id string, managed *managedServer) {
	now := time.Now()
	managed.mu.Lock()
	snapshot := managed.server.Snapshot()
	desiredConnected := managed.desiredConnected
	autoReconnect := managed.autoReconnect
	shouldConnect := autoReconnect && desiredConnected &&
		snapshot.Registration == RegistrationRegistered &&
		(snapshot.Connection == ConnectionNotAttempted ||
			snapshot.Connection == ConnectionDisconnected ||
			snapshot.Connection == ConnectionUnavailable) &&
		(managed.nextAttempt.IsZero() || !now.Before(managed.nextAttempt))
	shouldHealth := managed.client != nil &&
		snapshot.Connection == ConnectionConnected &&
		(managed.nextHealth.IsZero() || !now.Before(managed.nextHealth))
	if shouldHealth {
		managed.nextHealth = now.Add(h.options.options.HealthInterval)
	}
	managed.mu.Unlock()

	if shouldHealth {
		if err := h.Health(ctx, id); err != nil {
			h.scheduleReconnect(managed, time.Now())
			return
		}
	}
	if shouldConnect {
		if err := h.Connect(ctx, id); err != nil {
			h.scheduleReconnect(managed, time.Now())
			return
		}
	}

	snapshot = managed.server.Snapshot()
	if snapshot.Connection == ConnectionConnected &&
		(snapshot.CapabilityState == CapabilitiesNotLoaded ||
			snapshot.CapabilityState == CapabilitiesStale) {
		if err := h.LoadCapabilities(ctx, id); err != nil {
			h.scheduleReconnect(managed, time.Now())
			return
		}
	}

	snapshot = managed.server.Snapshot()
	managed.mu.Lock()
	desired := managed.desiredEnabled
	managed.mu.Unlock()
	if desired &&
		snapshot.Connection == ConnectionConnected &&
		snapshot.CapabilityState == CapabilitiesLoaded &&
		snapshot.Enablement == EnablementDisabled {
		if _, err := h.Enable(id); err != nil {
			h.scheduleReconnect(managed, time.Now())
			return
		}
	}

	managed.mu.Lock()
	if managed.client != nil && managed.server.Snapshot().Connection == ConnectionConnected {
		managed.backoff = h.options.options.ReconnectMin
		managed.nextAttempt = time.Time{}
	}
	managed.mu.Unlock()
}

func (h *Host) Health(ctx context.Context, id string) error {
	managed, err := h.managed(id)
	if err != nil {
		return err
	}
	managed.mu.Lock()
	connection := managed.client
	snapshot := managed.server.Snapshot()
	if connection == nil || snapshot.Connection != ConnectionConnected {
		managed.mu.Unlock()
		return nil
	}
	managed.mu.Unlock()

	pinger, ok := connection.(interface {
		Ping(context.Context) error
	})
	if !ok {
		return nil
	}
	pingContext, cancel := context.WithTimeout(ctx, h.options.options.Timeout)
	err = pinger.Ping(pingContext)
	cancel()
	if err == nil {
		return nil
	}

	managed.mu.Lock()
	defer managed.mu.Unlock()
	if managed.client != connection {
		return nil
	}
	return h.fail(managed, classifyFailure(err), err)
}

func (h *Host) scheduleReconnect(managed *managedServer, now time.Time) {
	managed.mu.Lock()
	defer managed.mu.Unlock()
	if managed.backoff <= 0 {
		managed.backoff = h.options.options.ReconnectMin
	}
	managed.nextAttempt = now.Add(managed.backoff)
	managed.backoff = nextReconnectBackoff(
		managed.backoff,
		h.options.options.ReconnectMax,
	)
}

func (h *Host) supervisionInterval() time.Duration {
	interval := h.options.options.ReconnectMin / 2
	if interval < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	if interval > 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	return interval
}

func nextReconnectBackoff(current, maximum time.Duration) time.Duration {
	if current <= 0 {
		return maximum
	}
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}
