// Text an extension showed (an extText item, ctx.ui.showText): its lines
// as the terminal shows them, each with a tone the view colours, and the
// part shown while collapsed (tested from Go: server/web/transcript_test.go).
// Tones are this client's own tokens, never anything the extension sent.

export type Tone = "" | "add" | "del" | "hunk" | "meta";

// Lines shown while collapsed when the extension did not say.
export const DEFAULT_PREVIEW = 10;

// The lines that start a file's header in a git diff.
const HEADERS = ["diff --git", "index ", "--- ", "+++ ", "new file mode", "deleted file mode", "old mode", "new mode", "similarity index", "rename ", "copy ", "Binary files"];

// Terminal escape sequences (colours, titles), which the terminal drops too.
const ESCAPES = /\x1b(\[[0-?]*[ -\/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])/g;

// lines splits text for display: escape sequences, line ends and trailing
// blank lines dropped, tabs widened. Leading spaces stay (a diff's context
// lines start with one).
export function lines(text: string): string[] {
  const out = text
    .replace(ESCAPES, "")
    .split("\n")
    .map((l) => l.replace(/\r$/, "").replace(/\t/g, "   "));
  while (out.length && out[out.length - 1].trim() === "") out.pop();
  return out;
}

// tone is how a line of a unified diff shows: added, removed, a hunk
// marker or a file header. Text in any other lang is plain.
export function tone(line: string, lang?: string): Tone {
  if (lang !== "diff") return "";
  if (HEADERS.some((h) => line.startsWith(h))) return "meta";
  if (line.startsWith("@@")) return "hunk";
  if (line.startsWith("+")) return "add";
  if (line.startsWith("-")) return "del";
  return "";
}

// fold is what shows of n lines: all of them when open or short enough,
// else the first preview; hidden counts the rest.
export function fold(n: number, preview: number | undefined, open: boolean): { shown: number; hidden: number; foldable: boolean } {
  const p = preview && preview > 0 ? preview : DEFAULT_PREVIEW;
  const foldable = n > p;
  const shown = foldable && !open ? p : n;
  return { shown, hidden: n - shown, foldable };
}
