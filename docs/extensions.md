# Writing atto extensions

An extension is a TypeScript or JavaScript file that atto loads into every
session. It can watch and steer the agent's shell commands, add to prompts,
add slash commands, and show things in the TUI. atto compiles it with esbuild
and runs it in an embedded JavaScript engine (goja); there is no Node.js, so
`require`, `process` and npm modules that need Node are not available.

`atto extensions docs` prints this file; `atto extensions types` prints
`atto.d.ts`, the API's type declarations.

## Where they live

| Path | Source |
| --- | --- |
| `~/.atto/extensions/<name>.ts` or `.js` | user |
| `~/.atto/extensions/<name>/index.ts` or `index.js` | user (a folder: other files in it can be imported) |
| `<project>/.atto/extensions/...` (same shapes) | project |

`<project>` is the nearest directory above the working directory that holds
`.git`. The name is the file or folder name. Files ending in `.d.ts` and
names starting with `.` are ignored.

**Project extensions need approval**, since a repository brings them: run
`/extensions approve <name>` in the TUI or `atto extensions approve <name>`.
The approval covers the code as it is (the bundle's hash, including the
files it imports); any change needs approval again. An agent cannot approve
from its shell. User extensions need no approval.

To turn one off, add its name to `settings.json`:

```json
{ "extensions": { "disabled": ["noisy"], "timeout": 5 } }
```

## Load, reload, debug

- Extensions load when a session starts. After editing one, run `/reload`
  (or `atto reload` from the agent's shell): the old runtime is disposed of
  (`onDispose` callbacks run, timers stop, its status items and widgets go)
  and the new code runs. The reload report lists `changed extension <name>`.
- The "Loaded" block lists every extension: loaded (with its commands and
  events), failed (with the error, `file:line:col` for syntax errors, the
  source line for exceptions), needs approval, or disabled. After `atto
  reload`, failures also come back to the agent in the `[atto event]`.
- `atto extensions` lists them without running any; `atto context` too.
- `atto.log(...)` and `console.log(...)` append to `~/.atto/extensions.log`,
  as do errors from handlers.

## The shape

```ts
/// <reference path="./atto.d.ts" />
export default function (atto: Atto) {
  atto.on("tool_call", (e) => {
    if (/\bgit\s+push\b.*--force/.test(e.command)) {
      return { block: true, reason: "force-push is not allowed here" };
    }
  });

  atto.registerCommand("todo", {
    description: "Count TODOs in the project",
    handler: async (args, ctx) => {
      const r = await atto.exec("git grep -c TODO || true");
      ctx.ui.notify(r.stdout.trim() || "no TODOs");
    },
  });
}
```

atto writes `atto.d.ts` next to your extensions when it loads them; the
`reference` line gives editors the types. Types are erased, not checked.
The default export may be `async`; atto waits for it (up to the timeout).
Modern syntax works: esbuild lowers it to what the engine runs (ES2017,
plus async/await). Relative imports are bundled into one script.

## Events

`atto.on(event, handler)`; the handler gets `(event, ctx)` and may return a
Promise. Several handlers of an event run in registration order, and
extensions in load order (user before project, then by name).

| Event | Payload | Return |
| --- | --- | --- |
| `session_start` | `{reason}`: `startup`, `resume`, `clear` | ignored |
| `session_end` | `{reason}`: `exit`, `clear`, `resume`, `other` | ignored; atto waits up to 2 s in all |
| `turn_start` | `{prompt}` | ignored |
| `turn_end` | `{error: string \| null, aborted}` | ignored |
| `user_prompt` | `{prompt}` | a string (or `{context}`) to add to the prompt; `{block: true, reason}` rejects it |
| `tool_call` | `{toolName, command, description, timeout, background}` | `{block: true, reason}` (the model gets the reason), `{command}` to run another command |
| `tool_result` | `{toolName, command, description, output, exitCode, timedOut, canceled, durationMs, job}` | a string (or `{output}`) to replace what the model receives |

`tool_call`, `tool_result` and `user_prompt` are waited for; the others are
not. A later handler sees what an earlier one changed (a rewritten command,
a redacted output).

**Order with Claude Code hooks.** Extensions run inside the hooks:
`UserPromptSubmit` hooks, then `user_prompt`; `PreToolUse` hooks, then
`tool_call`, the command, `tool_result`, then `PostToolUse` hooks (which
see the rewritten output, so a redaction reaches them too). A hook that
blocks wins before extensions see the call.

## The context and the UI

`ctx` (also `atto.ui`, `atto.session`, `atto.cwd` outside handlers):

- `ctx.hasUI`: true in the TUI, false in `atto -p` and the server.
- `ctx.cwd`, `ctx.session.id`, `ctx.session.model` (`provider/id`).
- `ctx.ui.notify(text, level?)`: `info` (default), `warning`, `error`.
- `ctx.ui.setStatus(key, text | null)`: an item in the status line.
- `ctx.ui.setWidget(key, lines[] | null)`: lines shown above the input.
- `ctx.ui.select(title, options)`: `Promise<string | undefined>`.
- `ctx.ui.confirm(text)`: `Promise<boolean>`.
- `ctx.ui.input(prompt)`: `Promise<string | undefined>`.

Without a UI (`atto -p`, the server), `notify` goes to stderr (`-p`) or to
the client as an `extension/notify` notification (server); status items and
widgets are dropped; `select` and `input` resolve to `undefined` and
`confirm` to `false` at once. In the TUI, a dialog asked while another
dialog is open gets that default answer too. Time the user spends on a
dialog does not count against the handler timeout.

## Other APIs

- `atto.registerCommand(name, {description?, handler(args, ctx)})`: a
  slash command (`/name args`) in the TUI's command list, marked as the
  extension's. A built-in command of the same name wins.
- `atto.exec(command, {cwd?, timeout?})`: runs `command` with the agent's
  shell (bash, PowerShell on Windows); resolves to `{stdout, stderr, code,
  killed}`. `timeout` is in ms (default 60000); at it the command and its
  children are killed (`code` -1). The environment has `ATTO_SESSION_ID`
  and `ATTO_EXTENSION`.
- `atto.fs.readFile(path)`, `writeFile(path, text)` (creates parent
  directories), `exists(path)`, `list(dir)` (sorted names, directories end
  in `/`): synchronous, UTF-8, relative to the session's directory; errors
  throw.
- `fetch(url, {method?, headers?, body?, timeout?})` (also `atto.fetch`):
  resolves to `{status, ok, headers, text(), json()}`; rejects on network
  errors only. Bodies over 10 MiB are cut.
- `atto.sendMessage(text)`: a user message to the model. In the TUI it
  steers a running turn or starts one; in `-p` and the server it steers the
  running turn.
- `atto.onDispose(fn)`: runs before the extension is unloaded (reload,
  exit); up to 1 s.
- `setTimeout`, `setInterval`, `clearTimeout`, `clearInterval`.
- `atto.log(...)`, `console.log(...)`: to `~/.atto/extensions.log`.
- `atto.name`: the extension's name.

## Limits

- Each extension has its own engine and goroutine; its code never runs in
  parallel with itself, so no locking is needed.
- A waited-for handler that does not answer within the timeout (5 s;
  `"extensions": {"timeout": seconds}`) is skipped with a notice.
- Script that runs longer than the timeout without yielding (a busy loop)
  is interrupted and the extension is disabled until the next reload.
  An exception in a handler is reported and that handler skipped; the
  extension stays.
- Nothing an extension does can crash atto.

## Example: redact secrets from command output

```ts
/// <reference path="./atto.d.ts" />
export default function (atto: Atto) {
  const secret = /\b(sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{36})\b/g;
  atto.on("tool_result", (e) => e.output.replace(secret, "[redacted]"));
}
```

## Example: a status item

```ts
/// <reference path="./atto.d.ts" />
export default function (atto: Atto) {
  const update = async () => {
    const r = await atto.exec("git status --porcelain");
    const n = r.stdout.split("\n").filter(Boolean).length;
    atto.ui.setStatus("dirty", n ? `${n} changed` : null);
  };
  atto.on("session_start", update);
  atto.on("turn_end", update);
}
```
