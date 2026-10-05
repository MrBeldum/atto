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
  // stream reopens where it left off.
  follow(from: number, on: (n: Notification) => void, onState: (open: boolean) => void) {
    this.close();
    this.last = from;
    const url = "/events?token=" + encodeURIComponent(this.token) + (from ? "&lastEventId=" + from : "");
    const es = new EventSource(url);
    this.es = es;
    es.onopen = () => onState(true);
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
      }, 2000);
    };
    es.onmessage = (e) => {
      const id = Number(e.lastEventId);
      if (id) this.last = id;
      try {
        on(JSON.parse(e.data));
      } catch {
        /* not ours */
      }
    };
  }

  private last = 0;
  private retry: ReturnType<typeof setTimeout> | undefined;

  close() {
    clearTimeout(this.retry);
    this.es?.close();
    this.es = null;
  }
}
