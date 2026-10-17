/**
 * agent.ts 把 Go 端 agent.Event 流折叠成视图模型。
 * 事件契约见 specs/agent-core.md；wire 类型直接复用生成的客户端类型
 * （`mygo generate` 产物，已入库），契约漂移由 tsc 把关。
 */
import type { Event, Message, Usage } from "./mygo";

export type AgentEvent = Event;
export type WireMessage = Message;
export type { Usage };

export interface MessageView {
  kind: "message";
  id: number;
  role: "user" | "assistant";
  text: string;
  streaming: boolean;
  error?: string;
}

export interface ToolView {
  kind: "tool";
  id: number;
  callId: string;
  name: string;
  args: string;
  result: string;
  isError: boolean;
  running: boolean;
}

export type ChatItem = MessageView | ToolView;

export interface ChatState {
  items: ChatItem[];
  running: boolean;
  usage?: Usage;
  /** agent_end 带来的终止原因（error 时有 errorMessage）。 */
  endError?: string;
}

export const initialChat: ChatState = { items: [], running: false };

let seq = 0;
const nextId = () => ++seq;

/** restore 把历史消息（Agent.messages 的返回）映射成视图。 */
export function restore(messages: WireMessage[]): ChatState {
  const state: ChatState = { items: [], running: false };
  for (const m of messages) {
    if (m.role === "user") {
      state.items.push({
        kind: "message",
        id: nextId(),
        role: "user",
        text: m.content.map((b) => b.text ?? "").join("\n"),
        streaming: false,
      });
    } else if (m.role === "assistant") {
      for (const b of m.content) {
        if (b.type === "text" && b.text) {
          state.items.push({
            kind: "message",
            id: nextId(),
            role: "assistant",
            text: b.text,
            streaming: false,
          });
        } else if (b.type === "toolCall") {
          state.items.push({
            kind: "tool",
            id: nextId(),
            callId: b.id ?? "",
            name: b.name ?? "",
            args: b.arguments ?? "",
            result: "",
            isError: false,
            // 结果由后续 toolResult 消息补齐；没有就是被截断的调用。
            running: true,
          });
        }
      }
    } else {
      for (const b of m.content) {
        if (b.type !== "toolResult") continue;
        const tool = findTool(state.items, b.toolCallId);
        if (tool) {
          tool.result = b.text ?? "";
          tool.isError = b.isError ?? false;
          tool.running = false;
        }
      }
    }
  }
  return state;
}

/** appendUser 在发送前乐观加入用户消息。 */
export function appendUser(state: ChatState, text: string): ChatState {
  return {
    ...state,
    items: [...state.items, { kind: "message", id: nextId(), role: "user", text, streaming: false }],
  };
}

/** apply 折叠一个事件；返回新状态（不可变更新）。 */
export function apply(state: ChatState, ev: AgentEvent): ChatState {
  switch (ev.type) {
    case "agent_start":
      return { ...state, running: true, endError: undefined };

    case "message_start": {
      const items = [...state.items];
      // message_start 总是 assistant；接到当前流式文本的宿主。
      items.push({ kind: "message", id: nextId(), role: "assistant", text: "", streaming: true });
      return { ...state, items };
    }

    case "text_delta": {
      const items = [...state.items];
      for (let i = items.length - 1; i >= 0; i--) {
        const item = items[i];
        if (item.kind === "message" && item.role === "assistant" && item.streaming) {
          items[i] = { ...item, text: item.text + (ev.text ?? "") };
          break;
        }
      }
      return { ...state, items };
    }

    case "tool_execution_start": {
      const items = [...state.items];
      items.push({
        kind: "tool",
        id: nextId(),
        callId: ev.toolCallId ?? "",
        name: ev.toolName ?? "",
        args: ev.args ?? "",
        result: "",
        isError: false,
        running: true,
      });
      return { ...state, items };
    }

    case "tool_execution_end": {
      const items = [...state.items];
      const tool = findTool(items, ev.toolCallId);
      if (tool) {
        const i = items.indexOf(tool);
        items[i] = {
          ...tool,
          result: ev.resultContent ?? "",
          isError: ev.isError ?? false,
          running: false,
        };
      }
      return { ...state, items };
    }

    case "message_end": {
      const items = [...state.items];
      for (let i = items.length - 1; i >= 0; i--) {
        const item = items[i];
        if (item.kind === "message" && item.role === "assistant" && item.streaming) {
          items[i] = {
            ...item,
            streaming: false,
            error: ev.stopReason === "error" ? ev.errorMessage || "请求失败" : undefined,
          };
          break;
        }
      }
      return { ...state, items, usage: ev.usage ?? state.usage };
    }

    case "agent_end": {
      const items = state.items.map((item) =>
        item.kind === "message" || item.kind === "tool"
          ? { ...item, streaming: false, running: false }
          : item,
      );
      return {
        ...state,
        items,
        running: false,
        usage: ev.usage ?? state.usage,
        endError: ev.stopReason === "error" ? ev.errorMessage || "运行失败" : undefined,
      };
    }

    default:
      return state;
  }
}

function findTool(items: ChatItem[], callId?: string): ToolView | undefined {
  if (!callId) return undefined;
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i];
    if (item.kind === "tool" && item.callId === callId) return item;
  }
  return undefined;
}

/** endEvent 合成通道异常中断时的兜底 agent_end（幂等收敛）。 */
export function endEvent(stopReason: AgentEvent["stopReason"] = "end_turn", errorMessage?: string): AgentEvent {
  return { type: "agent_end", sessionId: "", turnIndex: 0, stopReason, errorMessage };
}

/** formatUsage 渲染状态栏用量。 */
export function formatUsage(u?: Usage): string {
  if (!u) return "";
  const inSum = u.inputTokens + u.cacheReadTokens + u.cacheWriteTokens;
  return `↑${formatTokens(inSum)} ↓${formatTokens(u.outputTokens)}`;
}

function formatTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n);
}
