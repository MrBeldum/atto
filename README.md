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
| `ATTO_VERSION=edge` | installs the edge build (see below) |
| `ATTO_INSTALL_DIR=...` | installs somewhere else |

Running the script again is safe. It installs the latest release over the old one.

**Edge builds.** Every push to `main` that passes the tests replaces the [`edge` prerelease](https://github.com/sebastianrcnt/atto/releases/tag/edge), named like `v0.0.3-dev.14+abc1234` (the next patch version, 14 commits after the last tag, at commit `abc1234`). It's unreleased code and may break. Install it with `ATTO_VERSION=edge` (`$env:ATTO_VERSION = "edge"` in PowerShell), or build from source with `go install github.com/sebastianrcnt/atto/cmd/atto@main`.

### Update

```sh
atto update          # install the latest release
atto update -check   # only check
atto channel         # show which channel this binary follows
atto channel edge    # switch to edge builds
atto channel stable  # go back to tagged releases
```

The channel is part of the binary: release builds are `stable`, edge builds are `edge`, and `atto -version` shows which (`atto v0.0.3-dev.14+abc1234 (edge)`). `atto update` stays on the channel you're on. `atto channel <name>` installs the latest binary of that channel, which then follows it. Nothing is saved in settings. Going from edge back to stable installs the latest stable release even though its version number is lower, and says so: `Switched to stable: atto v0.0.3-dev.14 → v0.0.2`. Only `atto channel` does that. Builds from `go install` or a local `go build` have no channel (they report `dev`) and get no update notices.

Once a day atto asks the GitHub API whether a newer release exists on your channel, and mentions it when you start atto. It never updates itself on its own. To turn the check off, set `"updateCheck": false` in `~/.atto/settings.json`.

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
- `input`: `["text", "image"]` if it accepts images (default: text only; catalog models know this already)

## Use

```sh
atto                                  # interactive session
atto "fix the build"                  # interactive, starting with this message
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
| `Esc` `Esc` | on an empty prompt: open the session tree to go back to an earlier message and edit it |
| `Shift+Tab` | cycle reasoning effort |
| `Ctrl+T` | expand all thinking and command output (or click a block) |
| `Ctrl+C` | interrupt; clears the input when idle; quits when the input is empty |
| `Ctrl+V` / `Alt+V` | attach the image on the clipboard (use `Alt+V` where the terminal pastes text on `Ctrl+V`, as on Windows) |

Pasting the path of an image file, or dropping the file on the terminal, attaches it too. Images show as `[image 1: 1024x768 PNG]` in the input; delete the placeholder to drop the image. Images larger than 2048 pixels are scaled down. Clipboard images need `osascript` (macOS; `pngpaste` is used if installed), `wl-paste` or `xclip` (Linux), or PowerShell (Windows, WSL).

Pastes over 1000 characters show as `[Pasted Content 1234 chars]` and are sent in full.

### Slash commands

| Command | What it does |
| --- | --- |
| `/model` | switch model mid-session |
| `/effort` | set reasoning effort |
| `/compact` | compact the conversation now |
| `/copy` | copy the last answer; works over SSH in terminals with OSC 52 |
| `/context` | show what fills the context and how much is cached |
| `/resume` | resume a saved session |
| `/tree` | go back to any point of the session; earlier branches are kept |
| `/fork` | start a new session from an earlier message |
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

**Sessions** are JSONL files under `~/.atto/sessions/`. As in pi, entries form a tree: going back with `/tree` starts a new branch in the same file and keeps the old one. `atto history grep` searches every branch and marks entries on other branches; `-active` limits it to the current one.

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
| `settings.json` | default model and effort, renderer, status line, hooks, `updateCheck`, `doubleEscapeAction` (`tree`, `fork` or `none`) |
| `models.json` | your providers and models |
| `auth.json` | keys and logins (mode 0600) |
| `sessions/` | saved sessions |
| `images/` | images sent in sessions, by content hash |

## License

[MIT](LICENSE)
