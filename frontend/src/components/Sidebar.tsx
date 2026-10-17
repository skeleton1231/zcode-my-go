interface SessionInfo {
  id: string;
  title: string;
  workspace: string;
  updatedAt: string;
  running: boolean;
}

interface SidebarProps {
  sessions: SessionInfo[];
  activeId: string | null;
  onSelect: (id: string) => void;
  onNew: () => void;
  onDelete: (id: string) => void;
  onOpenSettings: () => void;
}

function timeAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return new Date(then).toLocaleDateString();
}

/** Sidebar：会话列表 + 新建 + 设置入口。 */
export function Sidebar({ sessions, activeId, onSelect, onNew, onDelete, onOpenSettings }: SidebarProps) {
  return (
    <aside className="sidebar">
      <div className="sidebar-header">
        <div className="brand">
          Zgo <small>coding agent</small>
        </div>
        <button className="btn small ghost" onClick={onOpenSettings} title="设置">
          设置
        </button>
      </div>
      <div className="session-list">
        {sessions.map((s) => (
          <div key={s.id} style={{ position: "relative" }}>
            <button className={`session-item${s.id === activeId ? " active" : ""}`} onClick={() => onSelect(s.id)}>
              <span className="title">{s.title || "新会话"}</span>
              <span className="delete" role="button" tabIndex={0} title="删除会话"
                onClick={(e) => {
                  e.stopPropagation();
                  onDelete(s.id);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.stopPropagation();
                    onDelete(s.id);
                  }
                }}
              >
                ✕
              </span>
              <span className="meta">
                {timeAgo(s.updatedAt)}
                {s.running ? " · 运行中" : ""} · {s.workspace}
              </span>
            </button>
          </div>
        ))}
      </div>
      <div className="sidebar-footer">
        <button className="btn ghost" style={{ width: "100%" }} onClick={onNew}>
          + 新会话
        </button>
      </div>
    </aside>
  );
}
