// What extensions show around the terminal's input (extension/ui): their
// widgets, lines above the input, and their status line items, here a
// quiet band over the composer. Plain text only.

import type { ExtensionUI } from "../types";

export default function ExtensionBar({ ui }: { ui: ExtensionUI | null }) {
  const widgets = ui?.widgets || [];
  const status = ui?.status || [];
  if (widgets.length === 0 && status.length === 0) return null;
  return (
    <div className="mx-auto w-full max-w-[820px] px-4 pt-2" aria-label="Extensions" role="status">
      {widgets.length > 0 && (
        <div className="max-h-32 overflow-y-auto font-mono text-[12px] leading-[1.55] whitespace-pre-wrap text-ink-2" style={{ overflowWrap: "anywhere" }}>
          {widgets.map((w) => (
            <div key={w.key} title={w.key}>
              {w.lines.map((l, i) => (
                <div key={i}>{l || " "}</div>
              ))}
            </div>
          ))}
        </div>
      )}
      {status.length > 0 && (
        <div className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5 pt-1 text-[11.5px] text-ink-3">
          {status.map((s) => (
            <span key={s.key} title={s.key} className="max-w-full truncate">
              {s.text}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
