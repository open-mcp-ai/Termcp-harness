package toolcall

// RetryPolicy must be explicit. A zero budget means automatic retry is not
// configured and is therefore denied.
type RetryPolicy struct {
	MaxAttempts int
}

// RetryContext describes safety evidence available for the next attempt. A
// query mechanism alone is insufficient for a side effect: the orchestrator
// must confirm that the previous attempt did not take effect.
type RetryContext struct {
	IdempotencyKey              string
	Deduplicates                bool
	OutcomeConfirmedNotExecuted bool
}

type RetryDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

func EvaluateAutoRetry(snapshot Snapshot, policy RetryPolicy, context RetryContext) RetryDecision {
	if policy.MaxAttempts <= 0 {
		return deny("automatic retry budget is not configured")
	}
	if snapshot.Status != StatusUnknown && snapshot.Status != StatusFailed {
		return deny("only failed or unknown outcomes can be retried automatically")
	}
	if snapshot.Attempts <= 0 {
		return deny("no dispatched attempt is recorded")
	}
	if snapshot.Attempts >= policy.MaxAttempts {
		return deny("automatic retry budget is exhausted")
	}

	switch snapshot.Effect {
	case EffectReadOnly:
		return allow("the call is declared read-only")
	case EffectIdempotentWrite:
		return allow("the call is declared idempotent")
	case EffectSideEffect:
		if context.OutcomeConfirmedNotExecuted {
			return allow("the previous attempt was confirmed not to execute")
		}
		if context.IdempotencyKey != "" {
			return allow("an idempotency key protects the next attempt")
		}
		if context.Deduplicates {
			return allow("the tool provides deduplication for the next attempt")
		}
		return deny("a side-effecting attempt has no verified retry safety")
	case EffectUnspecified:
		return deny("the tool effect class is unspecified")
	default:
		return deny("the tool effect class is invalid")
	}
}

func allow(reason string) RetryDecision {
	return RetryDecision{Allowed: true, Reason: reason}
}

func deny(reason string) RetryDecision {
	return RetryDecision{Allowed: false, Reason: reason}
}
