// Text an extension showed (ctx.ui.showText; /diff is one): the title and
// the extension's name over a card like CodeBlock's, the text as plain
// lines, coloured when it is a diff, cut to its first lines until opened,
// as the terminal's block is.

import { useMemo, useState } from "react";
import { fold, lines as split, tone, type Tone } from "../exttext";
import type { Item } from "../types";
import { CopyButton } from "./CodeBlock";
import { Chevron } from "./icons";

const TONES: Record<Tone, string> = {
  "": "",
  add: "bg-green-tint text-green",
  del: "bg-red-tint text-red",
  hunk: "text-accent-ink",
  meta: "text-ink-3",
};

export default function ExtText({ it }: { it: Item }) {
  const [open, setOpen] = useState(false);
  const all = useMemo(() => split(it.text || ""), [it.text]);
  const f = fold(all.length, it.preview, open);
  return (
    <div className="w-full" style={{ animation: "fade-up 300ms cubic-bezier(0.23,1,0.32,1) both" }}>
      <div className="overflow-hidden rounded-card bg-surface shadow-card">
        <div className="flex h-9 min-w-0 items-center gap-2 border-b border-line px-3 text-[12.5px]">
          <span aria-hidden className="shrink-0 font-semibold text-magenta">
            ±
          </span>
          <span className="min-w-0 truncate font-medium text-ink">{it.title || "text"}</span>
          {it.ext && <span className="shrink-0 truncate text-[12px] text-ink-3">{it.ext}</span>}
          <CopyButton text={it.text || ""} />
        </div>
        <pre className={`m-0 overflow-auto py-2.5 font-mono text-[12px] leading-[1.6] text-ink-2 ${open ? "max-h-[70vh]" : ""}`}>
          <code className="inline-block min-w-full">
            {all.slice(0, f.shown).map((l, i) => (
              <div key={i} className={"px-3 " + TONES[tone(l, it.lang)]}>
                {l || " "}
              </div>
            ))}
          </code>
        </pre>
        {f.foldable && (
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="flex h-9 w-full items-center justify-center gap-1.5 border-t border-line text-[12px] font-medium text-ink-3 transition-colors duration-100 hover:bg-hover hover:text-ink"
          >
            {open ? "Show less" : `${f.hidden.toLocaleString()} more line${f.hidden === 1 ? "" : "s"}`}
            <Chevron size={12} className="transition-transform duration-200" style={{ transform: open ? "rotate(180deg)" : "rotate(0)" }} />
          </button>
        )}
      </div>
    </div>
  );
}
