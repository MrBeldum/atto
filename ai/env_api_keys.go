package ai

import "os"

// Port of src/env-api-keys.ts (API key variables only; the ambient AWS and
// Google credential probes belong to APIs atto does not port).

var envApiKeyVars = map[string]string{
	"ant-ling":                   "ANT_LING_API_KEY",
	"qwen-token-plan":            "QWEN_TOKEN_PLAN_API_KEY",
	"qwen-token-plan-cn":         "QWEN_TOKEN_PLAN_CN_API_KEY",
	"qwen-token-plan-individual": "QWEN_TOKEN_PLAN_API_KEY",
	"openai":                     "OPENAI_API_KEY",
	"azure-openai-responses":     "AZURE_OPENAI_API_KEY",
	"nvidia":                     "NVIDIA_API_KEY",
	"deepseek":                   "DEEPSEEK_API_KEY",
	"google":                     "GEMINI_API_KEY",
	"google-vertex":              "GOOGLE_CLOUD_API_KEY",
	"groq":                       "GROQ_API_KEY",
	"cerebras":                   "CEREBRAS_API_KEY",
	"xai":                        "XAI_API_KEY",
	"typesafe":                   "TYPESAFE_API_KEY",
	"radius":                     "RADIUS_API_KEY",
	"openrouter":                 "OPENROUTER_API_KEY",
	"vercel-ai-gateway":          "AI_GATEWAY_API_KEY",
	"zai":                        "ZAI_API_KEY",
	"zai-coding-cn":              "ZAI_CODING_CN_API_KEY",
	"mistral":                    "MISTRAL_API_KEY",
	"minimax":                    "MINIMAX_API_KEY",
	"minimax-cn":                 "MINIMAX_CN_API_KEY",
	"moonshotai":                 "MOONSHOT_API_KEY",
	"moonshotai-cn":              "MOONSHOT_API_KEY",
	"huggingface":                "HF_TOKEN",
	"fireworks":                  "FIREWORKS_API_KEY",
	"together":                   "TOGETHER_API_KEY",
	"baseten":                    "BASETEN_API_KEY",
	"opencode":                   "OPENCODE_API_KEY",
	"opencode-go":                "OPENCODE_API_KEY",
	"kimi-coding":                "KIMI_API_KEY",
	"meta":                       "META_API_KEY",
	"cloudflare-workers-ai":      "CLOUDFLARE_API_KEY",
	"cloudflare-ai-gateway":      "CLOUDFLARE_API_KEY",
	"xiaomi":                     "XIAOMI_API_KEY",
	"xiaomi-token-plan-cn":       "XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"xiaomi-token-plan-ams":      "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"xiaomi-token-plan-sgp":      "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
}

// GetProviderEnvValue reads name from the provider-scoped env, then the
// process environment (src/utils/provider-env.ts).
func GetProviderEnvValue(name string, env ProviderEnv) string {
	if v := env[name]; v != "" {
		return v
	}
	return os.Getenv(name)
}

// ApiKeyEnvVars returns the variables that can hold provider's API key.
func ApiKeyEnvVars(provider string) []string {
	switch provider {
	case "github-copilot":
		return []string{"COPILOT_GITHUB_TOKEN"}
	case "anthropic":
		return []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	}
	if v, ok := envApiKeyVars[provider]; ok {
		return []string{v}
	}
	return nil
}

// FindEnvKeys lists the configured variables for provider.
func FindEnvKeys(provider string, env ProviderEnv) []string {
	var found []string
	for _, v := range ApiKeyEnvVars(provider) {
		if GetProviderEnvValue(v, env) != "" {
			found = append(found, v)
		}
	}
	return found
}

// GetEnvApiKey returns provider's API key from the environment.
func GetEnvApiKey(provider string, env ProviderEnv) string {
	for _, v := range FindEnvKeys(provider, env) {
		if provider == "anthropic" && v == "ANTHROPIC_AUTH_TOKEN" {
			continue
		}
		return GetProviderEnvValue(v, env)
	}
	return ""
}
