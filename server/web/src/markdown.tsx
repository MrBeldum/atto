// A small markdown renderer for the agent's answers: fenced code, headings,
// lists, quotes, rules, tables and paragraphs; inline code, bold, italics
// and links. It builds elements, never HTML strings, so the text cannot
// inject markup. Text that is still streaming renders as it stands (an
// unclosed fence is code to the end).

import type { ReactNode } from "react";
import CodeBlock from "./components/CodeBlock";

// _italics_ are left out: snake_case names would turn italic, and the
// lookbehind they need is missing in older Safari.
const INLINE = /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|\*\*([^*]+)\*\*|__([^_]+)__|\*([^*\s][^*]*)\*|()\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g;

function inline(text: string, key = 0): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let k = key * 1000;
  for (const m of text.matchAll(INLINE)) {
    const i = m.index ?? 0;
    if (i > last) out.push(text.slice(last, i));
    if (m[2] !== undefined) out.push(<code key={k++}>{m[2]}</code>);
    else if (m[3] !== undefined || m[4] !== undefined) out.push(<strong key={k++}>{inline(m[3] ?? m[4], k)}</strong>);
    else if (m[5] !== undefined) out.push(<em key={k++}>{inline(m[5], k)}</em>);
    else if (m[7] !== undefined)
      out.push(
        <a key={k++} href={m[8]} target="_blank" rel="noreferrer noopener">
          {m[7]}
        </a>,
      );
    else if (m[9] !== undefined)
      out.push(
        <a key={k++} href={m[9]} target="_blank" rel="noreferrer noopener">
          {m[9]}
        </a>,
      );
    last = i + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

const LIST = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function cells(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
}

export function Markdown({ text }: { text: string }) {
  const lines = text.split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let k = 0;
  while (i < lines.length) {
    const line = lines[i];
    const fence = line.match(/^\s*(```+|~~~+)\s*([\w+-]*)/);
    if (fence) {
      const close = fence[1];
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(close)) body.push(lines[i++]);
      i++; // the closing fence
      blocks.push(<CodeBlock key={k++} code={body.join("\n")} lang={fence[2] || undefined} />);
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      const level = Math.min(h[1].length, 4);
      const Tag = ("h" + level) as "h1";
      blocks.push(<Tag key={k++}>{inline(h[2], k)}</Tag>);
      i++;
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      blocks.push(<hr key={k++} />);
      i++;
      continue;
    }
    if (line.trimStart().startsWith(">")) {
      const body: string[] = [];
      while (i < lines.length && lines[i].trimStart().startsWith(">")) body.push(lines[i++].trimStart().replace(/^>\s?/, ""));
      blocks.push(
        <blockquote key={k++}>
          <Markdown text={body.join("\n")} />
        </blockquote>,
      );
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1])) {
      const head = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") rows.push(cells(lines[i++]));
      blocks.push(
        <table key={k++}>
          <thead>
            <tr>
              {head.map((c, j) => (
                <th key={j}>{inline(c, j)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, j) => (
              <tr key={j}>
                {r.map((c, n) => (
                  <td key={n}>{inline(c, n)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>,
      );
      continue;
    }
    const li = line.match(LIST);
    if (li) {
      const ordered = /\d/.test(li[2]);
      const items: string[] = [];
      const base = li[1].length;
      while (i < lines.length) {
        const m = lines[i].match(LIST);
        if (m && m[1].length <= base && /\d/.test(m[2]) === ordered) {
          items.push(m[3]);
          i++;
        } else if (lines[i].trim() !== "" && (/^\s+/.test(lines[i]) || !LIST.test(lines[i])) && items.length && !/^\s*(```|~~~)/.test(lines[i])) {
          // a continuation or a nested list: part of the last item
          if (!/^\s/.test(lines[i]) && lines[i - 1]?.trim() === "") break;
          items[items.length - 1] += "\n" + lines[i].replace(/^\s{1,4}/, "");
          i++;
        } else break;
      }
      const Tag = ordered ? "ol" : "ul";
      blocks.push(
        <Tag key={k++}>
          {items.map((t, j) => (
            <li key={j}>{t.includes("\n") ? <Markdown text={t} /> : inline(t, j)}</li>
          ))}
        </Tag>,
      );
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && !/^\s*(```|~~~|#{1,6}\s|>)/.test(lines[i]) && !(para.length && LIST.test(lines[i]))) para.push(lines[i++]);
    if (!para.length) para.push(lines[i++]);
    const parts: ReactNode[] = [];
    para.forEach((p, j) => {
      if (j) parts.push(<br key={"br" + j} />);
      parts.push(...inline(p, j + 1));
    });
    blocks.push(<p key={k++}>{parts}</p>);
  }
  return <>{blocks}</>;
}
