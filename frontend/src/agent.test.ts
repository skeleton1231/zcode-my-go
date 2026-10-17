import { describe, expect, it } from "vitest";
import type { Event, EventKind } from "./mygo";
import { appendUser, apply, formatUsage, initialChat, restore, type Usage } from "./agent";

/** ev 补齐线上恒有但断言无关的字段。 */
const ev = (partial: Partial<Event> & { type: EventKind }): Event => ({
  sessionId: "test",
  turnIndex: 0,
  ...partial,
});

const usage = (over: Partial<Usage> = {}): Usage => ({
  inputTokens: 0,
  outputTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  ...over,
});

describe("apply: 文本回合", () => {
  it("按序折叠 agent_start → text_delta → message_end → agent_end", () => {
    let s = apply(initialChat, ev({ type: "agent_start" }));
    expect(s.running).toBe(true);

    s = apply(s, ev({ type: "message_start" }));
    s = apply(s, ev({ type: "text_delta", text: "你好" }));
    s = apply(s, ev({ type: "text_delta", text: "，世界" }));
    expect(s.items).toHaveLength(1);
    expect(s.items[0]).toMatchObject({ kind: "message", role: "assistant", text: "你好，世界", streaming: true });

    s = apply(s, ev({ type: "message_end", stopReason: "end_turn", usage: usage({ inputTokens: 10, outputTokens: 5 }) }));
    s = apply(s, ev({ type: "agent_end", stopReason: "end_turn" }));
    expect(s.running).toBe(false);
    expect(s.items[0]).toMatchObject({ streaming: false });
    expect(s.usage).toMatchObject({ inputTokens: 10, outputTokens: 5 });
  });

  it("message_end 带 error 时保留错误信息", () => {
    let s = apply(initialChat, ev({ type: "agent_start" }));
    s = apply(s, ev({ type: "message_start" }));
    s = apply(s, ev({ type: "message_end", stopReason: "error", errorMessage: "boom" }));
    s = apply(s, ev({ type: "agent_end", stopReason: "error", errorMessage: "boom" }));
    expect(s.items[0].kind === "message" && s.items[0].error).toBe("boom");
    expect(s.endError).toBe("boom");
  });
});

describe("apply: 工具回合", () => {
  it("tool_execution_start/end 更新对应工具块", () => {
    let s = apply(initialChat, ev({ type: "agent_start" }));
    s = apply(s, ev({ type: "tool_execution_start", toolCallId: "c1", toolName: "read", args: '{"path":"a.go"}' }));
    expect(s.items).toHaveLength(1);
    expect(s.items[0]).toMatchObject({ kind: "tool", name: "read", running: true });

    s = apply(s, ev({ type: "tool_execution_end", toolCallId: "c1", resultContent: "contents", isError: false }));
    expect(s.items[0]).toMatchObject({ running: false, result: "contents" });
  });

  it("多工具并发时按 callId 定位", () => {
    let s = apply(initialChat, ev({ type: "agent_start" }));
    s = apply(s, ev({ type: "tool_execution_start", toolCallId: "a", toolName: "read", args: "" }));
    s = apply(s, ev({ type: "tool_execution_start", toolCallId: "b", toolName: "grep", args: "" }));
    s = apply(s, ev({ type: "tool_execution_end", toolCallId: "a", resultContent: "A" }));
    const b = s.items.find((i) => i.kind === "tool" && i.callId === "b");
    expect(b && b.kind === "tool" && b.running).toBe(true);
  });

  it("agent_end 强制收敛所有流式状态", () => {
    let s = apply(initialChat, ev({ type: "agent_start" }));
    s = apply(s, ev({ type: "message_start" }));
    s = apply(s, ev({ type: "tool_execution_start", toolCallId: "c1", toolName: "bash", args: "{}" }));
    s = apply(s, ev({ type: "agent_end", stopReason: "aborted" }));
    expect(s.items.every((i) => i.kind !== "message" || !i.streaming)).toBe(true);
    expect(s.items.every((i) => i.kind !== "tool" || !i.running)).toBe(true);
  });
});

describe("appendUser / restore", () => {
  it("appendUser 不可变追加", () => {
    const before = initialChat;
    const after = appendUser(before, "hi");
    expect(before.items).toHaveLength(0);
    expect(after.items).toHaveLength(1);
    expect(after.items[0]).toMatchObject({ role: "user", text: "hi" });
  });

  it("restore 还原用户、assistant 文本与工具结果", () => {
    const state = restore([
      { role: "user", content: [{ type: "text", text: "fix" }] },
      {
        role: "assistant",
        content: [
          { type: "text", text: "looking" },
          { type: "toolCall", id: "c1", name: "read", arguments: '{"path":"a"}' },
        ],
      },
      { role: "toolResult", content: [{ type: "toolResult", toolCallId: "c1", text: "body", isError: false }] },
    ]);
    expect(state.items.map((i) => i.kind)).toEqual(["message", "message", "tool"]);
    const tool = state.items[2];
    if (tool.kind === "tool") {
      expect(tool.name).toBe("read");
      expect(tool.result).toBe("body");
      expect(tool.running).toBe(false);
    } else {
      throw new Error("expected tool item");
    }
  });

  it("restore 对缺失结果的截断调用保持 running 语义", () => {
    const state = restore([
      {
        role: "assistant",
        content: [{ type: "toolCall", id: "c9", name: "write", arguments: "{}" }],
      },
    ]);
    const tool = state.items[0];
    expect(tool.kind === "tool" && tool.running).toBe(true);
  });
});

describe("formatUsage", () => {
  it("渲染输入输出 token", () => {
    expect(formatUsage(undefined)).toBe("");
    expect(formatUsage(usage({ inputTokens: 1500, outputTokens: 700 }))).toBe("↑1.5k ↓700");
  });
});
