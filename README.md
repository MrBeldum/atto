# atto

A small terminal coding harness. You bring your own API key or model server.

## Install

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/sebastianrcnt/atto/main/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://raw.githubusercontent.com/sebastianrcnt/atto/main/install.ps1 | iex
```

**With Go** (1.27+)

```sh
go install github.com/sebastianrcnt/atto/cmd/atto@latest
```

The install scripts:

1. download the binary for your platform from the [latest release](https://github.com/sebastianrcnt/atto/releases/latest);
2. check it against the release's `checksums.txt`;
3. install it to `~/.local/bin` (macOS and Linux) or `%LOCALAPPDATA%\Programs\atto` (Windows).

They don't need root. Two environment variables change what gets installed:

| Variable | Effect |
| --- | --- |
| `ATTO_VERSION=v0.1.0` | installs that release |
| `ATTO_INSTALL_DIR=...` | installs somewhere else |

Running the script again is safe. It installs the latest release over the old one.

### Update

```sh
atto update          # install the latest release
atto update -check   # only check
```

Once a day atto asks the GitHub API whether a newer release exists, and mentions it when you start atto. It never updates itself on its own. To turn the check off, set `"updateCheck": false` in `~/.atto/settings.json`.

If you installed with `go install` or Homebrew, update that way instead.

## Set up a model

Use a hosted provider:

```sh
atto auth set opencode       # OpenCode Zen API key
atto auth set opencode-go    # OpenCode Go API key
atto login openai            # Sign in with ChatGPT (subscription)
atto models                  # list what's available
```

Or use your own OpenAI-compatible server (llama.cpp, vLLM, Ollama, …) by adding it to `~/.atto/models.json`:

```json
{
  "providers": {
    "local": {
      "baseUrl": "http://localhost:8080/v1",
      "models": [
        { "id": "my-model", "contextWindow": 131072, "maxTokens": 32768 }
      ]
    }
  }
}
```

A provider can also set:

- `apiKey`: a literal key, or `"$ENV_VAR"` to read it from the environment
- `headers`: extra request headers
- `extraBody`: extra fields for the request body
- `api`: `"openai-completions"` (the default) or `"openai-responses"`

A model can set:

- `efforts`: the reasoning levels it supports
- `effortMap`: how atto's effort levels translate into what the server expects

## Use

```sh
atto                                  # interactive session
atto -m local/my-model                # pick the model
atto -c                               # continue the last session here
atto -resume                          # pick a saved session
atto -p "fix the failing test"        # one prompt, non-interactive
git diff | atto -p "review this"      # stdin is appended to the prompt
atto -p -output-format json "..."     # also: stream-json
atto -p -goal "make the tests pass" -goal-budget 200k
```

### Keys in the session

| Key | Action |
| --- | --- |
| `Enter` | send; while the agent works, steer it after its current step |
| `Tab` | queue a message for when the agent finishes |
| `Esc` | interrupt, or send pending steers now |
| `Shift+Tab` | cycle reasoning effort |
| `Ctrl+T` | expand all thinking and command output (or click a block) |
| `Ctrl+C` | interrupt; clears the input when idle; quits when the input is empty |

### Slash commands

| Command | What it does |
| --- | --- |
| `/model` | switch model mid-session |
| `/effort` | set reasoning effort |
| `/compact` | compact the conversation now |
| `/context` | show what fills the context and how much is cached |
| `/resume` | resume a saved session |
| `/name` | name the session |
| `/archive` | archive the session and start a new one |
| `/clear` | start a new session |
| `/goal <objective>` | keep working until the objective is done; also `pause`, `resume`, `clear`, `budget <n>` |
| `/jobs`, `/stop` | list or stop background jobs |
| `/timer`, `/timers` | wake the agent later, or list pending timers |
| `/quit` | exit atto |

## How it works

**One tool.** The model works through a single shell tool: bash on macOS and Linux, PowerShell on Windows. Everything else is a command it can run:

- `atto history grep` searches the session transcript, including turns that were compacted away.
- `atto job start` runs a command in the background.
- `atto monitor` and `atto timer` wake the agent when something happens.
- `atto goal complete` reports that a goal is done.

**Prefix-cache friendly.**

- The history is append-only.
- The system prompt and the tool schema don't change during a session.
- Compaction keeps the latest user messages plus a summary, the way codex does it. Run `/context` to see the cache hit rate.

**Sessions** are JSONL files under `~/.atto/sessions/`.

**Hooks** use the same format as Claude Code: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `PreCompact` and `SessionStart`.

- Put them in `~/.atto/settings.json` or in the project's `.atto/settings.json`.
- A hook that exits with code 2, or returns `{"decision": "block"}`, stops the action.

**Front end and back end are separate.** Both servers speak the same JSON-RPC protocol, built around threads, turns and items:

- `atto serve` serves it over HTTP + SSE and includes a web client, so you can use atto from a phone.
- `atto app-server` serves it over stdio.

## Safety

atto has **no permission prompts**. The model's commands run with your user's permissions. The only filters are hooks you configure.

For untrusted repositories or long unattended runs, run atto in a VM or container.

Commands that atto runs get `ATTO_AGENT=1` in their environment. With it set, atto refuses to start another agent, change credentials, or replace itself. This guards against accidents. It is not a sandbox.

## Configuration

Everything lives in `~/.atto`. Set `ATTO_DIR` to move it.

| Path | Contents |
| --- | --- |
| `settings.json` | default model and effort, renderer, status line, hooks, `updateCheck` |
| `models.json` | your providers and models |
| `auth.json` | keys and logins (mode 0600) |
| `sessions/` | saved sessions |

## License

[MIT](LICENSE)
