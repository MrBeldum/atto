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

They don't need root. Three environment variables change what gets installed:

| Variable | Effect |
| --- | --- |
| `ATTO_CHANNEL=edge` | installs the edge build (see below); `stable` is the default |
| `ATTO_VERSION=v0.1.0` | pins that exact release; wins over `ATTO_CHANNEL` |
| `ATTO_INSTALL_DIR=...` | installs somewhere else |

Running the script again is safe. It installs the newest build of the channel over the old one.

**Edge builds.** Every push to `main` that passes the tests replaces the [`edge` prerelease](https://github.com/sebastianrcnt/atto/releases/tag/edge), named like `v0.0.3-dev.14+abc1234` (the next patch version, 14 commits after the last tag, at commit `abc1234`). It's unreleased code and may break. Install it with `curl -fsSL https://raw.githubusercontent.com/sebastianrcnt/atto/main/install.sh | ATTO_CHANNEL=edge sh` (`$env:ATTO_CHANNEL = "edge"` before running `install.ps1` in PowerShell), or build from source with `go install github.com/sebastianrcnt/atto/cmd/atto@main`.

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
atto -p -image shot.png "why?"        # attach images (repeatable)
pngpaste - | atto -p "what is this?"  # an image on stdin is attached too
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
| `Ctrl+B` | move the running command to the background: it keeps running as a job (`/jobs`), the agent goes on and gets an `[atto event]` when it exits |
| `Ctrl+C` | copy the selection if there is one; otherwise interrupt, clear the input when idle, or quit when the input is empty |
| `Ctrl+V` / `Alt+V` | attach the image on the clipboard (use `Alt+V` where the terminal pastes text on `Ctrl+V`, as on Windows) |

Shell commands, as in pi: start the prompt with `!` to run a command yourself, in the working directory, with the shell and environment the agent's own commands use and its output cut (the full text is saved to a temp file). It shows in the conversation as a `! command` block, and the command and its output go to the model as a user message the next time it runs (`Ran` followed by the command and its output). `!!command` runs it the same way but keeps it from the model; the block says so. The input turns green while the text starts with `!`. `Esc` or `Ctrl+C` cancels the command. It runs at once even while the agent works, but its result joins the conversation only when the turn ends, so it never lands between a tool call and its result; one command runs at a time (another is refused and stays in the input). `!` alone is an ordinary message. Commands are saved in the session and come back on resume, `/tree` and fork. No hooks run for them, and `atto -p` has no such prefix.

Pasting the path of an image file, or dropping the file on the terminal, attaches it too. Images show as `[image 1: 1024x768 PNG]` in the input; delete the placeholder to drop the image. Images larger than 2048 pixels are scaled down. Clipboard images need `osascript` (macOS; `pngpaste` is used if installed), `wl-paste` or `xclip` (Linux), or PowerShell (Windows, WSL).

`atto -p` attaches images given with `-image` and an image piped to stdin (PNG, JPEG, GIF or WebP, recognized by its first bytes); the prompt argument is then the text. In `atto serve`'s web client, attach images with the `+` button, by pasting or by dropping them; over JSON-RPC, `turn/start` takes `images: [{mimeType, data}]` (base64 or a `data:` URL, at most 10 of 10 MB each). Either way the model must accept images.

Pastes over 1000 characters show as `[Pasted Content 1234 chars]` and are sent in full.

### Mouse and selection

In the fullscreen renderer atto handles the mouse itself: the wheel scrolls the conversation, and a click on a block's header or its `+ N lines` line expands or collapses it.

- Drag to select text anywhere in the conversation; it is copied when you let go. Dragging past the top or bottom scrolls.
- Double-click selects a word (a file path or a URL is one word), triple-click the line.
- With a selection showing, `Ctrl+C` copies it instead of interrupting and `Shift+arrows` extend it. `Esc`, `PageUp` and `PageDown` keep it; other keys and clicks clear it.
- Copying uses every way that applies: the system clipboard (`pbcopy`, `wl-copy`/`xclip`/`xsel` and the PRIMARY selection, PowerShell), the tmux paste buffer inside tmux, and OSC 52, which is what reaches your clipboard over SSH. A short note says which worked.

Inside tmux, atto only gets the mouse with `set -g mouse on` (it says so at startup when it is off). OSC 52 through tmux needs `set -g set-clipboard on` or `set -g allow-passthrough on`.

To use the terminal's own selection instead, hold the key that bypasses mouse reporting: `Shift` in most terminals (Windows Terminal, GNOME Terminal, Konsole, kitty, WezTerm, Alacritty), `Option` in iTerm2, `Fn` in Terminal.app. Over SSH or in tmux it is the key of the terminal you are sitting at. Or turn mouse handling off with `"mouse": false` in `settings.json` or `ATTO_NO_MOUSE=1`; scrolling then works with `PageUp`/`PageDown`.

### Slash commands

| Command | What it does |
| --- | --- |
| `/model` | switch model mid-session |
| `/effort` | set reasoning effort |
| `/tui [auto\|fullscreen\|inline]` | show or change the renderer; saved to `settings.json` and applied at once |
| `/compact` | compact the conversation now |
| `/copy` | copy the last answer; works over SSH in terminals with OSC 52 |
| `/context` | show what fills the context and how much is cached |
| `/reload` | read AGENTS.md, skills, hooks, `settings.json` and `models.json` again, keeping the conversation |
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
- `atto job start` runs a command in the background. With `-notify REGEXP` (and `-notify-limit N`, default 50) each matching output line wakes the agent while the job keeps running; matches within a second are batched.
- `atto monitor` and `atto timer` wake the agent when something happens. `atto timer every 30m [-count N] [-until HH:MM|duration] <message>` repeats (minimum 1m, no drift; missed intervals fire once).
- `atto goal complete` reports that a goal is done.
- `atto reload` reloads the session's AGENTS.md files, skills, hooks and settings after the agent edited them; the result comes back as an `[atto event]`.

**Nothing loads unseen.** When a session starts, resumes or forks, the conversation opens with a dim "Loaded" block: the AGENTS.md (or AGENTS.override.md, CLAUDE.md) files in the system prompt with their sizes (and whether the 32 KiB cap cut them), files that were found but skipped and why, the skills and where they came from, the hooks, the settings and models files read, and the model and effort with where each came from (`-m`, the session, `settings.json`). Click its header or press `Ctrl+T` for the full list. `/reload` shows it again with what changed. The same report:

- `atto context` prints it for the current directory (`-json` for the data).
- `atto -p -v` prints the one-line-per-kind summary to stderr at the start, and stream-json's `init` event has it as `context`.
- The server's `thread/start` and `thread/resume` results include it as `context`.

**Prefix-cache friendly.**

- The history is append-only.
- The system prompt and the tool schema don't change during a session, unless `/reload` (or `atto reload`) finds that AGENTS.md files or skills changed; the next request then reads the new prompt in full, and the reload says so.
- Compaction keeps the latest user messages plus a summary, the way codex does it. Run `/context` to see the cache hit rate.

**Sessions** are JSONL files under `~/.atto/sessions/`. As in pi, entries form a tree: going back with `/tree` starts a new branch in the same file and keeps the old one. When that leaves work behind, atto asks whether to summarize the branch being left (optionally with your own instructions); the current model writes the summary, `Esc` cancels it, and the model sees it on the new branch. `"branchSummary": {"skipPrompt": true}` in `settings.json` never asks. `atto history grep` searches every branch and marks entries on other branches; `-active` limits it to the current one.

Manage sessions from the shell, without the TUI:

```
atto resume [id]                     resume a session (no id opens the picker; an id may be a unique prefix)
atto sessions [-all] [-archived] [-json] [-n N]   list this directory's sessions (-all: every directory)
atto sessions show <id>              details and the last user messages
atto sessions rename <id> <name>
atto sessions archive|unarchive <id>
atto sessions delete [-y] <id>       permanent: also removes its jobs, inbox, goal and images no other session uses
```

`delete` asks first on a terminal and refuses without `-y` elsewhere. Inside an atto agent only `list` and `show` work, so a model can't destroy session history.

**Hooks** use the same format as Claude Code: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `PreCompact`, `SessionStart`, `SessionEnd` and `Notification`.

- Put them in `~/.atto/settings.json` or in the project's `.atto/settings.json`.
- A hook that exits with code 2, or returns `{"decision": "block"}`, stops the action.
- `Stop` runs when the agent is done answering (in the TUI, `-p` and the servers). Blocking it sends the reason to the model as a user message and the turn continues. The input has `stop_hook_active`, true once a Stop hook has already kept this turn going, so a hook can let it finish. After 8 blocks in a row atto stops anyway. An interrupted turn (Esc) runs no Stop hook. With `/goal`, the Stop hook runs at the end of every turn, before the goal decides whether to continue.
- `SessionEnd` runs when a session ends, with `reason`: `exit` (quitting the TUI), `clear` (`/clear`), `resume` (switching to another session), or `other` (`-p` finishing, the server shutting down, a conversation archived). It cannot block and has a 5 second default timeout (set `timeout` on the hook to change it), so exiting stays quick. The matcher is tested against `reason`.
- `Notification` runs when atto wants your attention, with `message` and `notification_type`; the matcher is tested against the type. It cannot block. The TUI sends `idle_prompt` when a turn that took 15 seconds or more is done and atto waits for your input (not when a queued message or a goal turn follows), `background_event` when a job or timer event arrives while atto is idle, and `goal_blocked` when a goal becomes blocked. There are no permission prompts, so no such notification. `-p` and the servers send none.
- Hook messages, such as the reason of a blocked Stop, appear in the transcript, and as `hook` events in `-p --output-format stream-json`.

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
| `settings.json` | default model and effort, renderer, `mouse`, status line, hooks, `updateCheck`, `doubleEscapeAction` (`tree`, `fork` or `none`), `branchSummary.skipPrompt`, `toolOutputTokenLimit` (how much of a command's output the model gets, default 10000 tokens; the middle is cut and the full output saved to a file, as in codex), `backgroundExit` (experimental: `false` turns off the exit menu that offers "Run in background" while a turn runs) |
| `models.json` | your providers and models |
| `auth.json` | keys and logins (mode 0600) |
| `sessions/` | saved sessions |
| `images/` | images sent in sessions, by content hash |

### Troubleshooting

If text looks garbled, doubled or leaves fragments behind (seen with wide characters such as Korean in Windows Terminal and other ConPTY hosts), atto can repaint every visible row on each frame instead of only the changed ones. This is on by default on Windows. Set `ATTO_FULL_REPAINT=1` to force it on, or `ATTO_FULL_REPAINT=0` to force it off, on any OS.

## Development

```sh
go install ./cmd/atto          # this machine
scripts/deploy.sh win linux    # other machines over ssh, no GitHub involved
```

`scripts/deploy.sh` builds an edge binary of this checkout for each host's system and installs it where the install scripts would (`%LOCALAPPDATA%\Programs\atto` on Windows, `~/.local/bin` elsewhere), so `atto update` there keeps following edge. Its version ends in `.local`.

## License

[MIT](LICENSE)
