// JSON-RPC over HTTP and notifications over SSE (server/transport.go).

import type { Notification } from "./types";

const KEY = "atto-token";

export function initialToken(): string {
  const fromHash = new URLSearchParams(location.hash.slice(1)).get("token");
  if (fromHash) return fromHash;
  try {
    return localStorage.getItem(KEY) || "";
  } catch {
    return "";
  }
}

export function saveToken(token: string) {
  try {
    if (token) localStorage.setItem(KEY, token);
    else localStorage.removeItem(KEY);
  } catch {
    /* private mode: the token lives in memory only */
  }
}

export class Unauthorized extends Error {}

let nextId = 0;

export class Client {
  token: string;
  private es: EventSource | null = null;
  onUnauthorized: () => void = () => {};

  constructor(token: string) {
    this.token = token;
  }

  async call<T = any>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    const r = await fetch("/rpc", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: "Bearer " + this.token },
      body: JSON.stringify({ jsonrpc: "2.0", id: ++nextId, method, params }),
    });
    if (r.status === 401) {
      this.onUnauthorized();
      throw new Unauthorized("unauthorized");
    }
    const j = await r.json();
    if (j.error) throw new Error(j.error.message);
    return j.result as T;
  }

  // follow (re)opens the event stream after event `from`. EventSource
  // reconnects by itself, with Last-Event-ID, while the server is away;
  // when it gives up (an error status), the token is checked: a revoked
  // one (/remote off or restarted) ends in onUnauthorized, else the
  // stream reopens where it left off, waiting longer each time it fails.
  // Events at or before the last one seen are skipped: a resume never
  // applies a delta twice.
  follow(from: number, on: (n: Notification) => void, onState: (open: boolean) => void) {
    this.close();
    this.last = from;
    this.on = on;
    this.onState = onState;
    const url = "/events?token=" + encodeURIComponent(this.token) + (from ? "&lastEventId=" + from : "");
    const es = new EventSource(url);
    this.es = es;
    es.onopen = () => {
      this.delay = 1000;
      onState(true);
    };
    es.onerror = () => {
      onState(false);
      if (es.readyState !== EventSource.CLOSED || this.es !== es) return;
      clearTimeout(this.retry);
      this.retry = setTimeout(() => {
        if (this.es !== es) return;
        this.call("initialize").then(
          () => this.es === es && this.follow(this.last, on, onState),
          (err) => !(err instanceof Unauthorized) && es.onerror?.(new Event("error")),
        );
      }, this.delay);
      this.delay = Math.min(this.delay * 2, 30000);
    };
    es.onmessage = (e) => {
      let n: Notification;
      try {
        n = JSON.parse(e.data);
      } catch {
        return; // not ours
      }
      // events/reset has no ID of its own (lastEventId is then the one
      // before it): never skipped.
      if (n.method !== "events/reset") {
        const id = Number(e.lastEventId);
        if (id && id <= this.last) return; // seen
        if (id) this.last = id;
      }
      on(n);
    };
  }

  // wake reopens the stream after the page was hidden for a while (a
  // phone asleep): a connection the OS dropped may never say so.
  wake(hiddenFor: number) {
    const es = this.es;
    if (!es || !this.on || !this.onState) return;
    if (es.readyState === EventSource.OPEN && hiddenFor < 20000) return;
    this.follow(this.last, this.on, this.onState);
  }

  private last = 0;
  private delay = 1000;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private on: ((n: Notification) => void) | null = null;
  private onState: ((open: boolean) => void) | null = null;

  close() {
    clearTimeout(this.retry);
    this.es?.close();
    this.es = null;
  }
}
