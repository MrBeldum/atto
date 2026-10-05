// The terminal's pickers and inputs, mirrored by /remote (prompt/open):
// a bottom sheet for a choice, a dialog for a line of input. Esc, the
// backdrop or the phone's back gesture cancel, as Esc does in the
// terminal. Styled like Beautiful UI's sheets and dialogs (see app.css).

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Prompt } from "../types";

export type Answer = { index: number } | { text: string } | { cancel: true };

// The prompt on screen (one at a time), and how many history.back()
// calls of ours have not popped yet.
let shown: { id: string; back: () => void } | null = null;
let ours = 0;
let listening = false;

function onPop() {
  if (ours > 0) {
    // Our own pop of a prompt that closed; a prompt that opened since
    // gets its entry back.
    ours--;
    if (shown) history.pushState({ prompt: shown.id }, "");
    return;
  }
  const s = shown;
  if (!s) return;
  shown = null;
  s.back();
}

// useBack cancels on the phone's back gesture: an open prompt is a
// history entry, popped again when it closes some other way. A prompt
// that follows another (the terminal asks the next question) takes over
// its entry, and the pop of a closed prompt is never taken for the back
// gesture on the next one, which would cancel it.
function useBack(id: string, onBack: () => void) {
  const back = useRef(onBack);
  back.current = onBack;
  useLayoutEffect(() => {
    if (!listening) {
      listening = true;
      window.addEventListener("popstate", onPop);
    }
    const me = { id, back: () => back.current() };
    shown = me;
    if (history.state?.prompt) history.replaceState({ prompt: id }, "");
    else history.pushState({ prompt: id }, "");
    return () => {
      if (shown !== me) return; // popped by the back gesture
      shown = null;
      // Later, so a prompt opening in the same update takes the entry.
      setTimeout(() => {
        if (shown === null && history.state?.prompt) {
          ours++;
          history.back();
        }
      }, 0);
    };
  }, [id]);
}

function useEscape(onEsc: () => void) {
  const esc = useRef(onEsc);
  esc.current = onEsc;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        esc.current();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

export default function PromptSheet({ prompt, onAnswer }: { prompt: Prompt; onAnswer: (a: Answer) => void }) {
  const cancel = () => onAnswer({ cancel: true });
  useBack(prompt.id, cancel);
  useEscape(cancel);
  return prompt.kind === "input" ? <InputDialog prompt={prompt} onAnswer={onAnswer} /> : <SelectSheet prompt={prompt} onAnswer={onAnswer} />;
}

function Backdrop({ onClick }: { onClick: () => void }) {
  return <div className="fixed inset-0 z-40 bg-black/40" style={{ animation: "fade-in 160ms ease-out both" }} onClick={onClick} />;
}

function Heading({ prompt }: { prompt: Prompt }) {
  return (
    <div className="min-w-0">
      <div className="text-[15px] leading-snug font-semibold text-ink" style={{ overflowWrap: "anywhere" }}>
        {prompt.title || "Choose"}
      </div>
      {prompt.subtitle && (
        <div className="mt-0.5 line-clamp-3 text-[13px] leading-snug text-ink-3" style={{ overflowWrap: "anywhere" }}>
          {prompt.subtitle}
        </div>
      )}
      <div className="mt-1 text-[11.5px] text-ink-3">from the terminal</div>
    </div>
  );
}

function SelectSheet({ prompt, onAnswer }: { prompt: Prompt; onAnswer: (a: Answer) => void }) {
  const options = prompt.options || [];
  const [query, setQuery] = useState("");
  const searchable = prompt.filterable || options.length > 12;
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = options.map((o, i) => ({ ...o, i }));
    return q ? all.filter((o) => (o.label + " " + (o.description || "")).toLowerCase().includes(q)) : all;
  }, [options, query]);
  const list = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    list.current?.querySelector<HTMLElement>("[data-selected]")?.scrollIntoView({ block: "center" });
  }, [prompt.id]);

  return (
    <>
      <Backdrop onClick={() => onAnswer({ cancel: true })} />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={prompt.title}
        className="fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[78vh] w-full max-w-[560px] flex-col rounded-t-window bg-surface shadow-overlay"
        style={{ paddingBottom: "env(safe-area-inset-bottom)", animation: "sheet-up 260ms cubic-bezier(0.23,1,0.32,1) both" }}
      >
        <div className="mx-auto mt-2 h-1 w-9 shrink-0 rounded-full bg-line-strong" />
        <div className="flex shrink-0 items-start gap-3 px-4 pt-2.5 pb-3">
          <div className="min-w-0 flex-1">
            <Heading prompt={prompt} />
          </div>
          <button
            type="button"
            onClick={() => onAnswer({ cancel: true })}
            className="h-8 shrink-0 rounded-control px-2.5 text-[13px] font-medium text-ink-2 transition-colors hover:bg-hover active:scale-[0.97]"
          >
            Cancel
          </button>
        </div>
        {searchable && (
          <div className="shrink-0 px-4 pb-2">
            <input
              value={query}
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              placeholder="Search"
              autoComplete="off"
              autoCapitalize="off"
              spellcheck={false}
              className="h-10 w-full rounded-control border border-line bg-field px-3 text-[16px] text-ink outline-none placeholder:text-ink-3 focus:border-line-strong"
            />
          </div>
        )}
        <div ref={list} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-3">
          {shown.length === 0 && <p className="px-3 py-3 text-[13px] text-ink-3">No matches.</p>}
          {shown.map((o) => {
            const sel = o.i === prompt.selected;
            return (
              <button
                key={o.i}
                type="button"
                data-selected={sel ? "" : undefined}
                onClick={() => onAnswer({ index: o.i })}
                className={`mb-0.5 flex w-full min-h-11 items-start gap-2.5 rounded-control px-3 py-2.5 text-left transition-colors hover:bg-hover active:bg-hover-2 ${sel ? "bg-accent-tint" : ""}`}
              >
                <span className={`mt-[7px] size-1.5 shrink-0 rounded-full ${sel ? "bg-accent" : "bg-transparent"}`} />
                <span className="min-w-0 flex-1">
                  <span className={`block text-[14.5px] leading-snug ${sel ? "font-medium text-accent-ink" : "text-ink"}`} style={{ overflowWrap: "anywhere" }}>
                    {o.label}
                  </span>
                  {o.description && (
                    <span className="mt-0.5 block text-[12.5px] leading-snug text-ink-3" style={{ overflowWrap: "anywhere" }}>
                      {o.description}
                    </span>
                  )}
                </span>
              </button>
            );
          })}
          {prompt.note && <p className="px-3 pt-1 text-[12px] text-ink-3">{prompt.note}</p>}
        </div>
      </div>
    </>
  );
}

function InputDialog({ prompt, onAnswer }: { prompt: Prompt; onAnswer: (a: Answer) => void }) {
  const [text, setText] = useState(prompt.text || "");
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);
  return (
    <>
      <Backdrop onClick={() => onAnswer({ cancel: true })} />
      <form
        role="dialog"
        aria-modal="true"
        aria-label={prompt.title}
        onSubmit={(e) => {
          e.preventDefault();
          onAnswer({ text });
        }}
        className="fixed inset-x-4 top-[12vh] z-50 mx-auto max-w-[460px] rounded-window bg-surface p-4 shadow-overlay"
        style={{ animation: "pop-in 200ms cubic-bezier(0.23,1,0.32,1) both" }}
      >
        <Heading prompt={prompt} />
        <input
          ref={ref}
          value={text}
          onInput={(e) => setText((e.target as HTMLInputElement).value)}
          placeholder={prompt.placeholder || ""}
          autoComplete="off"
          className="mt-3 h-11 w-full rounded-control border border-line bg-field px-3 text-[16px] text-ink outline-none placeholder:text-ink-3 focus:border-line-strong"
        />
        <div className="mt-3 flex justify-end gap-2">
          <button
            type="button"
            onClick={() => onAnswer({ cancel: true })}
            className="h-10 rounded-control bg-surface px-4 text-[14px] font-medium text-ink-2 shadow-btn active:scale-[0.97]"
          >
            Cancel
          </button>
          <button type="submit" className="h-10 rounded-control bg-ink px-4 text-[14px] font-medium text-surface active:scale-[0.97]">
            Submit
          </button>
        </div>
      </form>
    </>
  );
}
