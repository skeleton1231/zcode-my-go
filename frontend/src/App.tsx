import { useCallback, useEffect, useRef, useState } from "react";
import { Channel } from "mygo-runtime";
import { Agent, events, type Message, type SessionInfo, type SettingsView } from "./mygo";
import {
  appendUser,
  apply,
  endEvent,
  formatUsage,
  initialChat,
  restore,
  type AgentEvent,
  type ChatState,
} from "./agent";
import { Composer } from "./components/Composer";
import { MessageItem } from "./components/MessageItem";
import { SettingsModal } from "./components/SettingsModal";
import { Sidebar } from "./components/Sidebar";
import { ToolBlock } from "./components/ToolBlock";

export default function App() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [chat, setChat] = useState<ChatState>(initialChat);
  const [settings, setSettings] = useState<SettingsView | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickBottom = useRef(true);

  const refreshSessions = useCallback(async () => {
    setSessions(await Agent.listSessions());
  }, []);

  // 会话索引：初始加载 + 订阅 Go 端广播。
  useEffect(() => {
    void refreshSessions();
    const off = events.agentSessionsChanged.on((list: SessionInfo[]) => setSessions(list));
    return () => off();
  }, [refreshSessions]);

  useEffect(() => {
    void Agent.getSettings().then(setSettings);
  }, []);

  // 切换会话：恢复历史。
  const openSession = useCallback(async (id: string) => {
    setActiveId(id);
    const messages: Message[] = await Agent.messages(id);
    setChat(restore(messages));
    stickBottom.current = true;
  }, []);

  const newSession = useCallback(async () => {
    const workspace = await Agent.pickWorkspace();
    if (!workspace) return;
    const info = await Agent.newSession(workspace);
    await refreshSessions();
    await openSession(info.id);
  }, [openSession, refreshSessions]);

  const deleteSession = useCallback(
    async (id: string) => {
      await Agent.deleteSession(id);
      if (id === activeId) {
        setActiveId(null);
        setChat(initialChat);
      }
    },
    [activeId],
  );

  // 跟随滚动：只有本来就在底部时才吸底。
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onScroll = () => {
      stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickBottom.current) el.scrollTop = el.scrollHeight;
  }, [chat.items]);

  const send = useCallback(
    async (text: string) => {
      if (!activeId) return;
      setChat((state) => appendUser(state, text));
      stickBottom.current = true;

      const channel = new Channel<AgentEvent>();
      const done = Agent.chat(activeId, text, channel);
      try {
        for await (const ev of channel) setChat((state) => apply(state, ev));
        await done;
      } catch (err) {
        // 通道异常（会话不存在等）不会有 agent_end 事件，兜底收敛。
        const message = err instanceof Error ? err.message : String(err);
        setChat((state) => apply(state, endEvent("error", message)));
      }
    },
    [activeId],
  );

  const stop = useCallback(() => {
    if (activeId) void Agent.abort(activeId);
  }, [activeId]);

  const saveSettings = useCallback(
    async (baseUrl: string, apiKey: string, model: string) => {
      setSettings(await Agent.saveSettings(baseUrl, apiKey, model));
    },
    [],
  );

  const needsSetup = settings !== null && (!settings.hasApiKey || !settings.model);

  return (
    <div className={`app${sessions.length === 0 ? " no-sidebar" : ""}`}>
      {sessions.length > 0 && (
        <Sidebar
          sessions={sessions}
          activeId={activeId}
          onSelect={(id) => void openSession(id)}
          onNew={() => void newSession()}
          onDelete={(id) => void deleteSession(id)}
          onOpenSettings={() => setSettingsOpen(true)}
        />
      )}
      <main className="main">
        {activeId ? (
          <div className="chat-scroll" ref={scrollRef}>
            <div className="chat-inner">
              {chat.items.map((item) =>
                item.kind === "message" ? (
                  <MessageItem key={item.id} msg={item} />
                ) : (
                  <ToolBlock key={item.id} tool={item} />
                ),
              )}
              {chat.endError && (
                <div className="msg-assistant">
                  <div className="error-banner">{chat.endError}</div>
                </div>
              )}
            </div>
          </div>
        ) : (
          <div className="chat-scroll">
            <div className="empty">
              <h1>Zgo</h1>
              <p style={{ margin: 0 }}>
                选择一个工作区，把任务交给 agent：读代码、改文件、跑命令。
              </p>
              <div style={{ display: "flex", gap: 12 }}>
                <button className="btn primary" onClick={() => void newSession()}>
                  选择工作区开始
                </button>
                <button className="btn ghost" onClick={() => setSettingsOpen(true)}>
                  配置模型
                </button>
              </div>
              {needsSetup && (
                <p style={{ margin: 0, color: "var(--color-warn)" }}>
                  尚未配置 API Key 或模型，先在「配置模型」里完成设置。
                </p>
              )}
              <p style={{ margin: 0 }}>
                <kbd>Enter</kbd> 发送 · <kbd>Shift</kbd>+<kbd>Enter</kbd> 换行
              </p>
            </div>
          </div>
        )}
        <Composer running={chat.running} disabled={!activeId} onSend={(t) => void send(t)} onStop={stop} />
        <footer className="statusbar">
          <span>
            <span className={`dot ${chat.running ? "running" : chat.endError ? "error" : ""}`} />
            {settings?.model || "未配置模型"}
          </span>
          <span className="mono">{formatUsage(chat.usage)}</span>
        </footer>
      </main>
      <SettingsModal
        open={settingsOpen}
        settings={settings}
        onClose={() => setSettingsOpen(false)}
        onSave={saveSettings}
      />
    </div>
  );
}
