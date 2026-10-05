// The transcript store: items by ID in the order they started, updated in
// place as notifications arrive (tested from Go: server/web/transcript_test.go).
//
// The view renders it in blocks of CHUNK items, each memoized on its
// array: a delta makes a new array for its own block only, so a frame
// re-renders one block, not thousands of items.

import type { BlockDisplay, Item } from "./types";

export const CHUNK = 50;

// A running command's output is kept as the server keeps it (see
// core/transcript): the last KEEP_OUTPUT bytes, trimmed once it doubles.
export const KEEP_OUTPUT = 64 * 1024;

export class Transcript {
  private items: Item[] = [];
  private index = new Map<string, number>();
  private chunks: Item[][] = [];
  private dirty = new Set<number>();
  // commands in progress, for running()
  private open = new Set<string>();
  notes = 0;

  reset(items: Item[] = []) {
    this.items = [];
    this.index.clear();
    this.chunks = [];
    this.dirty.clear();
    this.open.clear();
    items.forEach((it) => this.upsert(it));
  }

  get length() {
    return this.items.length;
  }

  get(id: string): Item | undefined {
    const i = this.index.get(id);
    return i === undefined ? undefined : this.items[i];
  }

  last(): Item | undefined {
    return this.items[this.items.length - 1];
  }

  upsert(it: Item) {
    let i = this.index.get(it.id);
    if (i === undefined) {
      i = this.items.length;
      this.index.set(it.id, i);
      this.items.push(it);
    } else this.items[i] = it;
    this.dirty.add(Math.floor(i / CHUNK));
    if (it.type === "commandExecution" && it.status === "inProgress" && !it.pending) this.open.add(it.id);
    else this.open.delete(it.id);
  }

  // delta appends streamed text (output, for a command) to an item; one
  // not known (not started yet as far as this client knows) is skipped.
  delta(id: string, d: string) {
    const it = this.get(id);
    if (!it) return;
    if (it.type === "commandExecution") {
      let out = (it.output || "") + d;
      if (out.length > 2 * KEEP_OUTPUT) out = out.slice(out.length - KEEP_OUTPUT);
      this.upsert({ ...it, output: out });
    } else this.upsert({ ...it, text: (it.text || "") + d });
  }

  // display sets what extensions show on an item (item/display; null:
  // nothing). The server sends it in order with the item's own
  // notifications, so a later item/completed carries it too.
  display(id: string, d: BlockDisplay | null) {
    const it = this.get(id);
    if (it) this.upsert({ ...it, display: d });
  }

  note(text: string, tone: "error" | "info" = "info") {
    this.upsert({ id: "note-" + ++this.notes, type: "note", text, tone });
  }

  list(): Item[] {
    return this.items.slice();
  }

  // blocks are the items in runs of CHUNK; a run is a new array only when
  // one of its items changed since the last call.
  blocks(): Item[][] {
    const n = Math.ceil(this.items.length / CHUNK);
    this.chunks.length = n;
    for (const j of this.dirty) if (j < n) this.chunks[j] = this.items.slice(j * CHUNK, (j + 1) * CHUNK);
    this.dirty.clear();
    return this.chunks;
  }

  running(): boolean {
    return this.open.size > 0;
  }
}
