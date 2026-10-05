// atto's web client: atto serve's threads, or with /remote the terminal's
// own live session. See server/protocol.go for the protocol.

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, Bolt, Branch, Flag, Info, Layers, Menu, Plus, Radio, Target } from "./components/icons";
import Loading from "./components/Loading";
import PromptBar, { type Pending } from "./components/PromptBar";
import Thinking from "./components/Thinking";
import ToolRow from "./components/ToolRow";
import { Markdown } from "./markdown";
import { Client, initialToken, saveToken, Unauthorized } from "./rpc";
import type { Item, Model, Notification, ThreadInfo, ThreadSummary } from "./types";

// --- items ---

function secs(ms?: number) {
  if (!ms) return "";
  const s = ms / 1000;
  return s < 1 ? "<1s" : s < 60 ? `${Math.round(s)}s` : `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
}

function Line({ icon, children, tone = "text-ink-3" }: { icon: any; children: any; tone?: string }) {
  return (
    <div className={`flex min-h-7 items-start gap-2 text-[13px] leading-[1.5] ${tone}`}>
      <span className="mt-[3px] flex shrink-0">{icon}</span>
      <span className="min-w-0 whitespace-pre-wrap" style={{ overflowWrap: "anywhere" }}>
        {children}
      </span>
    </div>
  );
}

const Caret = () => (
  <span className="ml-0.5 inline-block h-[1em] w-0.5 translate-y-[3px] rounded-full bg-ink" style={{ animation: "caret-blink 1s step-end infinite" }} />
);

const ItemView = memo(function ItemView({ it }: { it: Item }) {
  const working = it.status === "inProgress";
  switch (it.type) {
    case "userMessage":
      return (
        <div className="flex justify-end pl-10" style={{ animation: "fade-up 300ms cubic-bezier(0.23,1,0.32,1) both" }}>
          <div className="max-w-full rounded-[18px] bg-field px-3.5 py-2 text-[15px] leading-normal whitespace-pre-wrap text-ink shadow-hairline" style={{ overflowWrap: "anywhere" }}>
            {it.text}
          </div>
        </div>
      );
    case "agentMessage":
      return (
        <div className="prose-atto text-ink">
          <Markdown text={it.text || ""} />
          {working && <Caret />}
        </div>
      );
    case "reasoning":
      return (
        <Thinking working={working} done={it.durationMs ? `Thought for ${secs(it.durationMs)}` : "Thought"}>
          {it.text}
        </Thinking>
      );
    case "commandExecution":
      return <ToolRow it={it} />;
    case "compaction":
      return (
        <Thinking
          working={working}
          icon={<Layers size={15} />}
          active="Compacting context"
          done={"Context compacted" + (it.tokensBefore ? ` · ${it.tokensBefore.toLocaleString()} → ~${(it.tokensAfter || 0).toLocaleString()} tokens` : "")}
        >
          {it.text}
        </Thinking>
      );
    case "branchSummary":
      return (
        <Thinking working={working} icon={<Branch size={15} />} active="Summarizing branch" done="Branch summary">
          {it.text}
        </Thinking>
      );
    case "event":
      return <Line icon={<Bolt size={13} />}>{(it.text || "").replace(/^\[atto event\] ?/, "").split("\n")[0]}</Line>;
    case "goal":
      return <Line icon={<Target size={13} />}>{/<objective>/.test(it.text || "") ? "Continuing goal" : (it.text || "").replace(/^\[atto goal\] ?/, "").split("\n")[0]}</Line>;
    case "goalStatus":
      return <Line icon={<Target size={13} />}>{"Goal " + (it.goalStatus || "") + (it.text ? ": " + it.text : "")}</Line>;
    case "hook":
      return (
        <Line icon={<Flag size={13} />} tone={it.blocked ? "text-orange" : "text-ink-3"}>
          {(it.hookEvent || "hook") + ": " + (it.text || "")}
        </Line>
      );
    case "notice":
      return <Line icon={<Info size={13} />}>{it.text}</Line>;
    case "note":
      return (
        <Line icon={<Info size={13} />} tone={it.tone === "error" ? "text-red" : "text-ink-3"}>
          {it.text}
        </Line>
      );
  }
  return null;
});

// --- the transcript store: items by ID in order, updated in place and
// rendered once per frame however many deltas arrive ---

class Transcript {
  order: string[] = [];
  byId = new Map<string, Item>();
  notes = 0;

  reset(items: Item[] = []) {
    this.order = [];
    this.byId.clear();
    items.forEach((it) => this.upsert(it));
  }
  upsert(it: Item) {
    if (!this.byId.has(it.id)) this.order.push(it.id);
    this.byId.set(it.id, it);
  }
  delta(id: string, d: string) {
    const it = this.byId.get(id);
    if (!it) return;
    if (it.type === "commandExecution") this.byId.set(id, { ...it, output: (it.output || "") + d });
    else this.byId.set(id, { ...it, text: (it.text || "") + d });
  }
  note(text: string, tone: "error" | "info" = "info") {
    this.upsert({ id: "note-" + ++this.notes, type: "note", text, tone });
  }
  list(): Item[] {
    return this.order.map((id) => this.byId.get(id)!);
  }
  running(): boolean {
    for (const it of this.byId.values()) if (it.type === "commandExecution" && it.status === "inProgress" && !it.pending) return true;
    return false;
  }
}

// --- app ---

type Phase = "login" | "loading" | "ready";

function shortPath(p: string) {
  return p.replace(/^\/(Users|home)\/[^/]+/, "~");
}

function baseName(p: string) {
  return p.split(/[\\/]/).filter(Boolean).pop() || p;
}

export default function App() {
  const client = useMemo(() => new Client(initialToken()), []);
  const [phase, setPhase] = useState<Phase>(client.token ? "loading" : "login");
  const [loginError, setLoginError] = useState("");
  const [live, setLive] = useState(false);
  const [models, setModels] = useState<Model[]>([]);
  const [threads, setThreads] = useState<ThreadSummary[]>([]);
  const [info, setInfo] = useState<ThreadInfo | null>(null);
  const [connected, setConnected] = useState(true);
  const [drawer, setDrawer] = useState(false);
  const [busySince, setBusySince] = useState(Date.now());
  const [newModel, setNewModel] = useState(""); // for the next thread/start
  const [, setTick] = useState(0);
  const store = useRef(new Transcript()).current;
  const infoRef = useRef<ThreadInfo | null>(null);
  infoRef.current = info;
  const logRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [away, setAway] = useState(false);

  // One render per frame for the transcript.
  const frame = useRef(0);
  const redraw = useCallback(() => {
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      setTick((t) => t + 1);
    });
  }, []);

  const note = useCallback(
    (text: string, tone: "error" | "info" = "info") => {
      store.note(text, tone);
      redraw();
    },
    [store, redraw],
  );

  const fail = useCallback(
    (e: unknown) => {
      if (e instanceof Unauthorized) return;
      note(String((e as Error)?.message || e), "error");
    },
    [note],
  );

  client.onUnauthorized = () => {
    client.close();
    saveToken("");
    setLoginError(live ? "This link is no longer valid: /remote was turned off or restarted. Scan the new code." : "The token was not accepted.");
    setPhase("login");
  };

  // Notifications.
  const onNote = useCallback(
    (n: Notification) => {
      const p = n.params || {};
      const cur = infoRef.current;
      if (n.method === "thread/switched") {
        if (live) openLive();
        return;
      }
      if (!cur || p.threadId !== cur.threadId) {
        if (n.method === "turn/completed" && !live) loadThreads();
        return;
      }
      switch (n.method) {
        case "turn/started":
          setInfo((i) => (i ? { ...i, busy: true, turnId: p.turnId } : i));
          setBusySince(Date.now());
          break;
        case "item/started":
        case "item/updated":
        case "item/completed":
          store.upsert(p.item);
          redraw();
          break;
        case "item/delta":
          store.delta(p.itemId, p.delta);
          redraw();
          break;
        case "turn/completed":
          setInfo((i) => (i ? { ...i, busy: false, turnId: "", contextTokens: p.contextTokens ?? i.contextTokens } : i));
          // The terminal says so itself in a live session (as notices).
          if (!live && p.status === "failed") note("Failed: " + (p.error || "unknown error"), "error");
          else if (!live && p.status === "interrupted") note("Interrupted.");
          if (!live) loadThreads();
          break;
        case "thread/updated":
          if (p.thread) setInfo((i) => (i ? { ...i, ...p.thread, items: undefined } : i));
          break;
        case "hook":
          if (!p.turnId) note("⚑ " + p.event + ": " + p.message);
          break;
        case "extension/notify":
          note((p.extension ? p.extension + ": " : "") + p.message, p.level === "error" ? "error" : "info");
          break;
        case "thread/reloaded":
          note(p.error ? "Reload failed: " + p.error : "Reloaded configuration.", p.error ? "error" : "info");
          break;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [live],
  );
  const onNoteRef = useRef(onNote);
  onNoteRef.current = onNote;

  const follow = useCallback(
    (from: number) => client.follow(from, (n) => onNoteRef.current(n), setConnected),
    [client],
  );

  const show = useCallback(
    (t: ThreadInfo) => {
      store.reset(t.items || []);
      setInfo({ ...t, items: undefined });
      if (t.busy) setBusySince(Date.now());
      stick.current = true;
      setDrawer(false);
      follow(t.eventId || 0);
      redraw();
    },
    [store, follow, redraw],
  );

  const loadThreads = useCallback(() => {
    client
      .call<{ threads: ThreadSummary[] }>("thread/list")
      .then((r) => setThreads(r.threads || []))
      .catch(() => {});
  }, [client]);

  const openLive = useCallback(() => {
    client.call<ThreadInfo>("thread/read").then(show).catch(fail);
  }, [client, show, fail]);

  // Start: check the token, then load models and the thread(s).
  const start = useCallback(async () => {
    setPhase("loading");
    try {
      const init = await client.call<{ live?: boolean; eventId?: number }>("initialize");
      saveToken(client.token);
      history.replaceState(null, "", location.pathname);
      setLive(!!init.live);
      const ms = await client.call<{ models: Model[] }>("models/list").catch(() => ({ models: [] as Model[] }));
      setModels(ms.models || []);
      setPhase("ready");
      if (init.live) {
        const t = await client.call<ThreadInfo>("thread/read");
        show(t);
      } else {
        follow(init.eventId || 0);
        loadThreads();
      }
    } catch (e) {
      if (!(e instanceof Unauthorized)) {
        setLoginError(String((e as Error).message || e));
        setPhase("login");
      }
    }
  }, [client, show, follow, loadThreads]);

  useEffect(() => {
    if (client.token) start();
    return () => client.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Scrolling: follow new output while at the bottom.
  const onScroll = () => {
    const el = logRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    stick.current = atBottom;
    setAway(!atBottom);
  };
  useLayoutEffect(() => {
    const el = logRef.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  });
  const toBottom = () => {
    const el = logRef.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    stick.current = true;
    setAway(false);
  };

  // Actions.
  const fallback = models.find((m) => m.hasKey)?.id || "";
  const startModel = newModel || fallback;
  const current = models.find((m) => m.id === (info ? info.model : startModel));
  const imagesOK = !!current?.images;

  const send = async (text: string, images: Pending[]): Promise<boolean> => {
    const imgs = images.map(({ mimeType, data }) => ({ mimeType, data }));
    try {
      let t = info;
      if (!t) {
        t = await client.call<ThreadInfo>("thread/start", startModel ? { model: startModel } : {});
        show(t);
        loadThreads();
      }
      stick.current = true;
      if (!live && t.busy) {
        if (imgs.length) {
          note("Images go with a new turn: send them once this one finishes.");
          return false;
        }
        await client.call("turn/steer", { threadId: t.threadId, input: text });
        note("↳ steering: " + text);
        return true;
      }
      const r = await client.call<{ status?: string }>("turn/start", { threadId: t.threadId, input: text, images: imgs });
      if (r.status === "steered") note("↳ sent to the running turn (after its next command): " + text);
      if (r.status === "queued") note("Queued: starts when the current run ends.");
      return true;
    } catch (e) {
      fail(e);
      return false;
    }
  };

  const setModel = async (id: string) => {
    if (!info) return;
    try {
      const t = await client.call<ThreadInfo>("thread/setModel", { threadId: info.threadId, model: id });
      setInfo((i) => (i ? { ...i, ...t, items: undefined } : i));
    } catch (e) {
      fail(e);
    }
  };
  const setEffort = async (effort: string) => {
    if (!info) return;
    try {
      const t = await client.call<ThreadInfo>("thread/setEffort", { threadId: info.threadId, effort });
      setInfo((i) => (i ? { ...i, ...t, items: undefined } : i));
    } catch (e) {
      fail(e);
    }
  };

  if (phase === "login") return <Login error={loginError} onToken={(t) => ((client.token = t), start())} />;
  if (phase === "loading")
    return (
      <div className="flex h-full items-center justify-center">
        <Loading label="Connecting" since={Date.now()} />
      </div>
    );

  const items = store.list();
  const busy = !!info?.busy;
  const last = items[items.length - 1];
  const streaming = last && last.status === "inProgress";
  const pickable = models.filter((m) => m.hasKey || m.id === info?.model);
  const title = info ? info.name || baseName(info.cwd) : "New conversation";
  const pct = info?.contextWindow ? Math.min(100, Math.round((100 * info.contextTokens) / info.contextWindow)) : 0;

  const toolbar = (
    <>
      {pickable.length > 0 && (
        <select
          aria-label="Model"
          value={info ? info.model : startModel}
          onChange={(e) => {
            const id = (e.target as HTMLSelectElement).value;
            if (info) setModel(id);
            else setNewModel(id);
          }}
          className="h-9 max-w-[42vw] min-w-0 appearance-none truncate rounded-chip bg-transparent px-2 text-[12.5px] font-medium text-ink-2 transition-colors hover:bg-hover disabled:opacity-60 sm:max-w-[260px]"
        >
          {pickable.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name} ({m.id.split("/")[0]})
            </option>
          ))}
        </select>
      )}
      {info?.efforts && info.efforts.length > 0 && (
        <select
          aria-label="Effort"
          value={info.effort}
          onChange={(e) => setEffort((e.target as HTMLSelectElement).value)}
          className="h-9 min-w-0 appearance-none rounded-chip bg-transparent px-2 text-[12.5px] text-ink-3 transition-colors hover:bg-hover"
        >
          {info.efforts.map((x) => (
            <option key={x}>{x}</option>
          ))}
        </select>
      )}
    </>
  );

  return (
    <div className="flex h-full">
      {!live && (
        <>
          {drawer && <div className="fixed inset-0 z-20 bg-black/30 md:hidden" onClick={() => setDrawer(false)} />}
          <aside
            className={`fixed inset-y-0 left-0 z-30 flex w-[84vw] max-w-[300px] flex-col border-r border-line bg-canvas transition-transform duration-200 md:static md:w-[280px] md:translate-x-0 ${drawer ? "translate-x-0" : "-translate-x-full"}`}
            style={{ paddingTop: "env(safe-area-inset-top)" }}
          >
            <div className="flex h-14 items-center gap-2 px-4">
              <span className="flex-1 text-[15px] font-semibold tracking-wide">atto</span>
              <button
                type="button"
                onClick={async () => {
                  try {
                    const id = info?.model || startModel;
                    show(await client.call<ThreadInfo>("thread/start", id ? { model: id } : {}));
                    loadThreads();
                  } catch (e) {
                    fail(e);
                  }
                }}
                className="flex h-9 items-center gap-1.5 rounded-control bg-surface px-3 text-[13px] font-medium shadow-btn active:scale-[0.97]"
              >
                <Plus size={14} /> New
              </button>
            </div>
            <div className="flex-1 overflow-y-auto px-2 pb-4">
              {threads.length === 0 && <p className="px-3 py-2 text-[13px] text-ink-3">No conversations yet.</p>}
              {threads.map((t) => (
                <button
                  key={t.threadId}
                  type="button"
                  onClick={() =>
                    client
                      .call<ThreadInfo>("thread/resume", { threadId: t.threadId })
                      .then(show)
                      .catch(fail)
                  }
                  className={`mb-0.5 block w-full rounded-control px-3 py-2.5 text-left transition-colors hover:bg-hover-2 ${t.threadId === info?.threadId ? "bg-hover-2" : ""}`}
                >
                  <div className="truncate text-[14px] text-ink">{t.name || t.preview || "(empty)"}</div>
                  <div className="truncate text-[12px] text-ink-3">
                    {t.updatedAt ? new Date(t.updatedAt).toLocaleString() + " · " : ""}
                    {shortPath(t.cwd)}
                  </div>
                </button>
              ))}
            </div>
          </aside>
        </>
      )}

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex shrink-0 items-center gap-2 border-b border-line bg-page/90 px-3 backdrop-blur" style={{ paddingTop: "env(safe-area-inset-top)" }}>
          <div className="flex h-12 w-full min-w-0 items-center gap-2">
            {!live && (
              <button type="button" aria-label="Conversations" onClick={() => setDrawer(true)} className="-ml-1 flex size-10 items-center justify-center rounded-control text-ink-2 hover:bg-hover md:hidden">
                <Menu size={18} />
              </button>
            )}
            <div className="min-w-0 flex-1">
              <div className="truncate text-[14px] font-medium text-ink">{title}</div>
              {info && (
                <div className="truncate text-[11.5px] text-ink-3">
                  {(live ? "terminal session · " : "") + shortPath(info.cwd)}
                  {pct ? ` · context ${pct}%` : ""}
                </div>
              )}
            </div>
            {live && (
              <span className="flex shrink-0 items-center gap-1 rounded-chip bg-accent-tint px-2 py-1 text-[11.5px] font-medium text-accent-ink">
                <Radio size={12} /> live
              </span>
            )}
            <span title={connected ? "connected" : "reconnecting"} className={`size-2 shrink-0 rounded-full ${connected ? "bg-green" : "bg-orange"}`} style={connected ? undefined : { animation: "caret-blink 1s step-end infinite" }} />
          </div>
        </header>

        <div ref={logRef} onScroll={onScroll} className="relative min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto flex w-full max-w-[820px] flex-col gap-3 px-4 pt-4 pb-6">
            {!info && (
              <div className="py-16 text-center text-[14px] text-ink-3">{live ? "Waiting for the terminal session…" : "Pick a conversation, or write below to start one."}</div>
            )}
            {items.map((it) => (
              <ItemView key={it.id} it={it} />
            ))}
            {busy && !streaming && <Loading label="Working" since={busySince} />}
          </div>
        </div>

        <div className="relative shrink-0">
          {away && (
            <button
              type="button"
              onClick={toBottom}
              aria-label="Jump to bottom"
              className="absolute -top-12 left-1/2 z-10 flex size-10 -translate-x-1/2 items-center justify-center rounded-full bg-surface text-ink-2 shadow-raised"
            >
              <ArrowDown size={16} />
            </button>
          )}
          <PromptBar
            busy={busy}
            canBackground={busy && store.running()}
            imagesOK={imagesOK}
            placeholder={live ? "Message the terminal session" : "Message atto"}
            toolbar={toolbar}
            onSend={send}
            onStop={() => info && client.call("turn/interrupt", { threadId: info.threadId }).catch(fail)}
            onBackground={() => info && client.call("turn/background", { threadId: info.threadId }).catch(fail)}
            warn={(t) => note(t, "error")}
          />
        </div>
      </main>
    </div>
  );
}

function Login({ error, onToken }: { error: string; onToken: (t: string) => void }) {
  const [tok, setTok] = useState("");
  return (
    <div className="mx-auto flex h-full max-w-[420px] flex-col justify-center px-5">
      <h1 className="mb-1 text-[22px] font-semibold">atto</h1>
      <p className="mb-5 text-[14px] leading-relaxed text-ink-2">
        Open the link that <code className="font-mono text-[13px]">atto serve</code> or <code className="font-mono text-[13px]">/remote</code> printed, scan its QR code, or paste the token.
      </p>
      {error && <p className="mb-4 rounded-control bg-red-tint px-3 py-2 text-[13px] text-red">{error}</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (tok.trim()) onToken(tok.trim());
        }}
        className="flex gap-2"
      >
        <input
          value={tok}
          onInput={(e) => setTok((e.target as HTMLInputElement).value)}
          placeholder="token"
          autoComplete="off"
          autoCapitalize="off"
          spellcheck={false}
          className="h-11 min-w-0 flex-1 rounded-control border border-line bg-field px-3 font-mono text-[16px] outline-none focus:border-line-strong"
        />
        <button type="submit" className="h-11 rounded-control bg-ink px-4 text-[14px] font-medium text-surface active:scale-[0.97]">
          Open
        </button>
      </form>
    </div>
  );
}
