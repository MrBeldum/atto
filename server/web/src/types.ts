// The protocol's shapes (server/protocol.go).

export type ItemType =
  | "userMessage"
  | "reasoning"
  | "agentMessage"
  | "commandExecution"
  | "compaction"
  | "event"
  | "goal"
  | "hook"
  | "notice"
  | "goalStatus"
  | "branchSummary"
  | "note"; // made by this client: errors and status lines

export type Item = {
  id: string;
  type: ItemType;
  text?: string;
  status?: "inProgress" | "completed" | "failed";
  description?: string;
  command?: string;
  output?: string;
  exitCode?: number;
  durationMs?: number;
  timedOut?: boolean;
  job?: number;
  background?: string;
  pending?: boolean;
  auto?: boolean;
  tokensBefore?: number;
  tokensAfter?: number;
  hookEvent?: string;
  blocked?: boolean;
  goalStatus?: string;
  tone?: "error" | "info"; // notes
};

export type ThreadInfo = {
  threadId: string;
  cwd: string;
  name?: string;
  model: string;
  effort: string;
  efforts?: string[];
  contextWindow?: number;
  contextTokens: number;
  busy: boolean;
  turnId?: string;
  items?: Item[];
  eventId?: number;
  live?: boolean;
};

export type ThreadSummary = {
  threadId: string;
  name?: string;
  preview?: string;
  cwd: string;
  updatedAt?: string;
  messages?: number;
  loaded?: boolean;
  live?: boolean;
};

export type Model = {
  id: string;
  name: string;
  contextWindow?: number;
  efforts?: string[];
  hasKey: boolean;
  images?: boolean;
};

export type Notification = { method: string; params: Record<string, any> };
