// Adapted from Beautiful UI's CodeBlock (https://www.beautifului.dev, MIT,
// Copyright (c) 2026 Shane Levine; see THIRD_PARTY_NOTICES): the card with
// a file header and a Copy button, the mono body and its light syntax
// coloring. Line numbers and the diff view are left out; diffs color
// their +/- lines instead.

import { useCallback, useState, type ReactNode } from "react";
import { Check, Copy } from "./icons";

const KEYWORDS = new Set(["import", "from", "export", "default", "async", "function", "func", "const", "let", "var", "await", "return", "if", "else", "for", "while", "new", "throw", "try", "catch", "null", "nil", "true", "false", "undefined", "package", "type", "struct", "def", "class", "fn", "pub", "use"]);
const TOKEN = /("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|\b\d+(?:\.\d+)?\b|[A-Za-z_$][\w$]*)/g;

function highlight(text: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let last = 0;
  let k = 0;
  for (const m of text.matchAll(TOKEN)) {
    const idx = m.index ?? 0;
    const t = m[0];
    let style: Record<string, string | number> | null = null;
    if (/^["'`]/.test(t) || /^\d/.test(t)) style = { color: "var(--orange)" };
    else if (KEYWORDS.has(t)) style = { color: "var(--accent-ink)" };
    else if (text[idx + t.length] === "(") style = { color: "var(--ink)", fontWeight: 500 };
    if (!style) continue;
    if (idx > last) nodes.push(text.slice(last, idx));
    nodes.push(
      <span key={k++} style={style}>
        {t}
      </span>,
    );
    last = idx + t.length;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}

function diffTone(line: string): string {
  if (line.startsWith("+") && !line.startsWith("+++")) return "bg-green-tint text-green";
  if (line.startsWith("-") && !line.startsWith("---")) return "bg-red-tint text-red";
  if (line.startsWith("@@")) return "text-accent-ink";
  return "";
}

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = useCallback(() => {
    const done = () => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    };
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done, () => {});
      return;
    }
    // Plain http on a LAN address is not a secure context: no clipboard API.
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      done();
    } finally {
      ta.remove();
    }
  }, [text]);
  return (
    <button
      type="button"
      aria-label={label}
      onClick={copy}
      className={`-mr-1 ml-auto flex h-8 shrink-0 items-center gap-1 rounded-[6px] px-2 text-[12px] font-medium transition-colors duration-100 hover:bg-hover ${copied ? "text-green" : "text-ink-3 hover:text-ink"}`}
    >
      {copied ? <Check size={11} /> : <Copy size={11} />}
      {copied ? "Copied" : label}
    </button>
  );
}

export default function CodeBlock({ code, lang, title }: { code: string; lang?: string; title?: string }) {
  const lines = code.replace(/\n$/, "").split("\n");
  const diff = lang === "diff" || lang === "patch";
  const plain = code.length > 20000; // keep huge blocks cheap
  return (
    <div className="my-2 w-full overflow-hidden rounded-card bg-surface shadow-card">
      <div className="flex h-9 items-center gap-2 border-b border-line px-3 text-[12px]">
        <span className="truncate font-mono leading-none text-ink-3">{title || lang || "code"}</span>
        <CopyButton text={code} />
      </div>
      <pre className="m-0 max-h-[60vh] overflow-auto px-3 py-2.5 font-mono text-[12.5px] leading-[1.6] text-ink-2">
        {plain ? (
          <code>{code}</code>
        ) : (
          <code>
            {lines.map((line, i) => (
              <div key={i} className={diff ? diffTone(line) + " -mx-3 px-3" : ""}>
                {diff ? line || " " : line ? highlight(line) : " "}
              </div>
            ))}
          </code>
        )}
      </pre>
    </div>
  );
}
