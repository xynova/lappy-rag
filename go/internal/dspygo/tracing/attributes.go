package tracing

const (
	// OpenInferenceSpanKindKey is the OpenInference span kind attribute key.
	OpenInferenceSpanKindKey = "openinference.span.kind"

	// OpenInferenceSpanKindChain marks chain-level spans.
	OpenInferenceSpanKindChain = "CHAIN"

	// OpenInferenceSpanKindLLM marks LLM invocation spans.
	OpenInferenceSpanKindLLM = "LLM"

	// OpenInferenceSpanKindEvaluator marks evaluator workflow spans.
	OpenInferenceSpanKindEvaluator = "EVALUATOR"
)
