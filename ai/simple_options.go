package ai

import "maps"

// Port of src/api/simple-options.ts.

const (
	contextSafetyTokens = 4096
	minMaxTokens        = 1
	// MinAnswerTokens are always left for the answer when a thinking budget
	// shares the response ceiling.
	MinAnswerTokens = 1024
)

// ClampMaxTokensToContext keeps the output cap within what is left of the
// context window. atto: a cap of 0 means "send none" and stays 0.
func ClampMaxTokensToContext(model *Model, context TranscriptContext, maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	if model.ContextWindow <= 0 {
		return max(minMaxTokens, maxTokens)
	}
	available := model.ContextWindow - EstimateContextTokens(context.Messages).Tokens - contextSafetyTokens
	return min(maxTokens, max(minMaxTokens, available))
}

// ResolveSamplingParams merges model, per-level and request sampling
// parameters, later ones winning per key.
func ResolveSamplingParams(model *Model, thinkingLevel string, request SamplingParams) SamplingParams {
	effective := ClampThinkingLevel(model, thinkingLevel)
	byLevel := model.SamplingParamsByThinkingLevel[effective]
	if model.SamplingParams == nil && byLevel == nil && request == nil {
		return nil
	}
	out := SamplingParams{}
	maps.Copy(out, model.SamplingParams)
	maps.Copy(out, byLevel)
	maps.Copy(out, request)
	return out
}

// BuildBaseOptions derives StreamOptions from SimpleStreamOptions.
func BuildBaseOptions(model *Model, context TranscriptContext, options *SimpleStreamOptions, apiKey string) StreamOptions {
	if options == nil {
		options = &SimpleStreamOptions{}
	}
	reasoning := options.Reasoning
	if reasoning == "" {
		reasoning = ThinkingOff
	}
	base := options.StreamOptions
	base.SamplingParams = ResolveSamplingParams(model, reasoning, options.SamplingParams)
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = model.MaxTokens
	}
	base.MaxTokens = ClampMaxTokensToContext(model, context, maxTokens)
	if apiKey != "" {
		base.APIKey = apiKey
	}
	return base
}

// DefaultThinkingBudgets are token budgets per level.
var DefaultThinkingBudgets = ThinkingBudgets{Minimal: 1024, Low: 2048, Medium: 8192, High: 16384}

// ClampReasoning maps xhigh and max to high for token-budget providers.
func ClampReasoning(effort string) string {
	if effort == ThinkingXHigh || effort == ThinkingMax {
		return ThinkingHigh
	}
	return effort
}

// ThinkingBudgetForLevel returns the token budget for a level.
func ThinkingBudgetForLevel(level string, custom *ThinkingBudgets) int {
	b := DefaultThinkingBudgets
	if custom != nil {
		if custom.Minimal > 0 {
			b.Minimal = custom.Minimal
		}
		if custom.Low > 0 {
			b.Low = custom.Low
		}
		if custom.Medium > 0 {
			b.Medium = custom.Medium
		}
		if custom.High > 0 {
			b.High = custom.High
		}
	}
	switch ClampReasoning(level) {
	case ThinkingMinimal:
		return b.Minimal
	case ThinkingLow:
		return b.Low
	case ThinkingMedium:
		return b.Medium
	case ThinkingHigh:
		return b.High
	}
	return 0
}

// ClampThinkingBudgetToAnswerRoom caps a budget so MinAnswerTokens remain.
func ClampThinkingBudgetToAnswerRoom(budget, ceiling int) int {
	return min(budget, max(0, ceiling-MinAnswerTokens))
}
