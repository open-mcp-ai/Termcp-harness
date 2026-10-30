package approval

// CorrelationCapability describes whether the connected Core has demonstrated
// support for correlating a pending MCP review with an approval request.
type CorrelationCapability string

const (
	// CorrelationCapabilityUnavailable means no approval coordinator is configured.
	CorrelationCapabilityUnavailable CorrelationCapability = "unavailable"
	// CorrelationCapabilityUnverified means the Core version cannot establish the
	// capability and no trusted correlation has been observed yet.
	CorrelationCapabilityUnverified CorrelationCapability = "unverified"
	// CorrelationCapabilityTrusted means at least one trusted approval reference
	// has been observed from the connected Core.
	CorrelationCapabilityTrusted CorrelationCapability = "trusted"
)
