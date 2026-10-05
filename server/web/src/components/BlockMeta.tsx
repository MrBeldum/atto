// What extensions show on a reasoning or answer block (item.display), as
// the terminal shows it: their statuses after the header ("· translating…")
// and, when one replaced the block's text, a line that flips the block
// between that text and the original. Only text: nothing an extension
// sends is markup.

import { useState } from "react";
import type { BlockDisplay } from "../types";

// useDisplay is the text a block shows, and its toggle state.
export function useDisplay(text: string | undefined, d?: BlockDisplay | null) {
  const [original, setOriginal] = useState(false);
  const replaced = !!d?.text;
  return {
    text: replaced && !original ? (d?.text as string) : text || "",
    replaced,
    original,
    toggle: () => setOriginal((o) => !o),
  };
}

// Statuses are the statuses after a header (lead: " · " first), or on a
// line of their own.
export function Statuses({ d, lead = true }: { d?: BlockDisplay | null; lead?: boolean }) {
  const s = d?.statuses;
  if (!s || s.length === 0) return null;
  return <span className="font-normal text-ink-3">{(lead ? " · " : "") + s.map((x) => x.text).join(" · ")}</span>;
}

export function OriginalToggle({ d, original, onToggle }: { d?: BlockDisplay | null; original: boolean; onToggle: () => void }) {
  if (!d?.text) return null;
  const ext = d.ext || "an extension";
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-pressed={original}
      className="-mx-1.5 flex min-h-7 w-fit max-w-full items-center rounded-control px-1.5 text-left text-[12px] text-ink-3 transition-colors duration-100 hover:bg-hover-2 hover:text-ink-2"
    >
      <span className="min-w-0 truncate">{original ? `Original shown · show ${ext}'s` : `Shown: ${ext} · show original`}</span>
    </button>
  );
}
