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
  | "extText"
  | "note"; // made by this client: errors and status lines

// What extensions show on a reasoning or agentMessage item: statuses for
// its header and a text shown in place of its own (by extension ext).
export type BlockDisplay = {
  statuses?: { ext: string; text: string }[];
  ext?: string;
  text?: string;
};

// What extensions show around the input: status items and widgets.
export type ExtensionUI = {
  status: { key: string; text: string }[];
  widgets: { key: string; lines: string[] }[];
};

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
  blockId?: string;
  display?: BlockDisplay | null;
  // extText
  title?: string;
  ext?: string;
  lang?: string;
  preview?: number;
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
  prompt?: Prompt;
  goal?: GoalInfo;
  extensionUi?: ExtensionUI;
};

// A picker or input open in the live session's terminal.
export type Prompt = {
  id: string;
  kind: "select" | "input";
  title: string;
  subtitle?: string;
  options?: { label: string; description?: string }[];
  selected: number;
  filterable?: boolean;
  total?: number;
  note?: string;
  text?: string;
  placeholder?: string;
};

// The live session's goal, worded as the terminal's status line.
export type GoalInfo = {
  objective: string;
  status: string;
  statusLabel: string;
  indicator: string;
  summary: string;
  note?: string;
  tokens: string;
  tokensUsed: number;
  budget?: number;
  elapsed: string;
  seconds: number;
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
