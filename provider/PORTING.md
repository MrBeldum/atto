# Porting from pi

atto's model layer is a Go port of pi's `pi-ai` package
(`packages/ai/src`) and the parts of `packages/coding-agent` that read
`auth.json` and `models.json`. Files map one to one, so a pi change can be
carried over by reading its TypeScript diff next to the Go file. pi is MIT
licensed; see `THIRD_PARTY_NOTICES`.

## Packages

- `ai`: pi-ai itself: types, event stream, api registry, the OpenAI APIs.
- `auth`: pi's `src/auth`: credential type, OAuth logins.
- `config`: what coding-agent does with `auth.json` and `models.json`.
- `provider`: atto's transcript types (stored in session files) and
  `Client`, which converts a transcript to an `ai.Context` and streams it
  with `ai.StreamSimple`.

## File mapping

| pi | atto |
|---|---|
| `ai/src/types.ts` | `ai/types.go` |
| `ai/src/utils/event-stream.ts` | `ai/event_stream.go` |
| `ai/src/compat.ts` (registry, `stream`, `complete`, `streamSimple`, `completeSimple`) | `ai/api_registry.go` |
| `ai/src/models.ts` (`calculateCost`, `getSupportedThinkingLevels`, `clampThinkingLevel`) | `ai/models.go` |
| `ai/src/env-api-keys.ts`, `utils/provider-env.ts` | `ai/env_api_keys.go` |
| `ai/src/api/transform-messages.ts` | `ai/transform_messages.go` |
| `ai/src/api/simple-options.ts` | `ai/simple_options.go` |
| `ai/src/api/openai-completions.ts` | `ai/openai_completions.go` |
| `ai/src/api/openai-responses.ts` | `ai/openai_responses.go` |
| `ai/src/api/openai-responses-shared.ts` | `ai/openai_responses_shared.go` |
| `ai/src/api/openai-codex-responses.ts` | `ai/openai_codex_responses.go` |
| `ai/src/api/openai-prompt-cache.ts`, `providers/openai.ts`, `openai-codex.ts`, `opencode.ts`, `opencode-go.ts`, `opencode-headers.ts` | `ai/providers.go` |
| `ai/src/utils/transcript.ts` | `ai/transcript.go` |
| `ai/src/utils/text.ts` | `ai/text.go` |
| `ai/src/utils/estimate.ts` | `ai/estimate.go` |
| `ai/src/utils/json-parse.ts` | `ai/json_parse.go` |
| `ai/src/utils/hash.ts` | `ai/hash.go` |
| `ai/src/utils/provider-retry.ts`, `utils/error-body.ts`, the openai SDK's HTTP and SSE handling | `ai/http.go` |
| `ai/src/auth/types.ts`, `auth/oauth/pkce.ts` | `auth/types.go` |
| `ai/src/auth/oauth/load.ts` | `auth/oauth.go` |
| `ai/src/auth/oauth/openai-chatgpt.ts` | `auth/openai_chatgpt.go` |
| `ai/src/auth/oauth/openai-codex.ts`, `oauth/device-code.ts`, `oauth/callback-server.ts` | `auth/openai_codex.go` |
| `coding-agent/src/core/auth-storage.ts` | `config/auth.go` |
| `coding-agent/src/core/model-config.ts`, `provider-composer.ts` (merging, overrides, key resolution) | `config/models.go` |
| `coding-agent/src/core/resolve-config-value.ts` | `config/resolve_config_value.go` |
| `coding-agent/src/modes/interactive/interactive-mode.ts` (`/login`, `/logout`), `core/auth-guidance.ts` | `app/login.go`, `core.NoModelsHint` |

## Go idioms for TypeScript

- Unions become interfaces (`ai.Message`, `ai.Content`) or string
  constants (`ai.StopReason`, event types). `UserMessage` content is
  `Text` (string form) or `Parts` (block form).
- `undefined` fields are zero values; `null` in a `thinkingLevelMap` is a
  nil `*string` (`ThinkingLevelMap.Lookup` tells the three cases apart).
- `AssistantMessageEventStream` is a goroutine-fed queue: range over
  `stream.All()`, then `stream.Result()`. `Partial` is the live message;
  read it only after the stream ended.
- `AbortSignal` is the options' `Context`; aborting yields stop reason
  `aborted`.
- Object literals pi sends keep their key order (`ai/ordered.go`). Request
  params are Go maps and marshal with sorted keys, as atto always did.

## Not ported

Anthropic, Google, Bedrock, Mistral and the other APIs; pi's `Models`
runtime and dynamic catalogs (atto's catalog comes from models.dev in
`config/catalog.go`); grammar-constrained custom tools, additional tools
and tool search; mid-conversation tool additions; GitHub Copilot headers;
the Codex WebSocket transport and zstd request compression (atto uses SSE);
images and classifier APIs.

## atto extensions

- `Model.Efforts`: an explicit list of levels (names such as `"on"` are
  allowed) used instead of pi's derivation.
- `Model.ExtraBody`: merged into chat completions bodies before the named
  fields, with `$effort`, `$thinking` and `$thinkingType` placeholders.
- `ToolCall.RawArguments`: the model's own argument bytes, replayed instead
  of re-serializing the parsed object.
- llama.cpp's `timings.cache_n` counts as cache reads.
- Keyless requests: no API key means no `Authorization` header (pi fails).
- Provider headers may use `"$session"` for the session ID.
- `models.json`: provider `env`, `maxTokensField`, `extraBody`; model
  `efforts`, `extraBody`; `effortMap` is read as `thinkingLevelMap`. A
  model configured without pi's fields (`reasoning`, `compat`) keeps
  atto's earlier wire format: system role, `max_tokens`, no `store`, effort
  only through `extraBody`. Models defined in `models.json` merge over
  catalog models with the same id (pi replaces them).
- `auth.json`: unreadable or unknown entries are skipped (pi rejects the
  file) and kept on rewrite.
- `provider.Message` records `provider`/`api`/`model`,
  `thinkingSignature` and `textSignature` so pi's same-model rules work on
  resumed sessions; messages written before count as the current model's.
