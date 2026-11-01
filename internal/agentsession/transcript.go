package agentsession

import "github.com/open-mcp-ai/termcp-harness/internal/model"

// Transcript is the bounded model-visible conversation retained for one
// Agent Session. It may contain sensitive user or tool data and must only be
// exposed through an authenticated control path.
type Transcript struct {
	Session  Snapshot        `json:"session"`
	Messages []model.Message `json:"messages"`
	Output   string          `json:"output,omitempty"`
}

func (s *Session) Transcript() Transcript {
	if s == nil {
		return Transcript{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Transcript{
		Session:  cloneSnapshot(s.snapshot),
		Messages: cloneModelMessages(s.transcript),
		Output:   s.finalOutput,
	}
}

func (s *Session) setPersistence(persist func() error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.persist = persist
	s.mu.Unlock()
}

func cloneModelMessages(messages []model.Message) []model.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]model.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		if message.ToolCalls != nil {
			cloned[i].ToolCalls = make([]model.ToolCall, len(message.ToolCalls))
			for j, call := range message.ToolCalls {
				cloned[i].ToolCalls[j] = call
				cloned[i].ToolCalls[j].Arguments = append([]byte(nil), call.Arguments...)
			}
		}
	}
	return cloned
}
