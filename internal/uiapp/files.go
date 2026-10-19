package uiapp

import (
	"github.com/egoist/mygo/ui"

	"github.com/skeleton1231/zcode-my-go/internal/tools"
)

// filesPanelView is the workspace file tree（ZCode browser-workbench 的
// file-tree 半边）：右列可开合，目录懒展开（ui.Tree/TreeItem），点文件
// 打开只读预览弹窗。数据经服务（path jail、本地与 SSH 远程同源）。
func (a *App) filesPanelView(c *ui.Context, t *ui.Theme) {
	if !a.filesOpen || a.activeID == "" {
		return
	}
	ui.Column(c).Width(280).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		panelHeader(c, t, iconFolder, "文件", func() {
			if iconButton(c, iconRefresh, "刷新", "重新读取工作区").Clicked() {
				a.treeEntries = map[string][]tools.DirEntry{}
				a.treeExpanded = map[string]bool{}
			}
			closeIconButton(c, func() { a.filesOpen = false })
		})
		ui.Scroll(c).Grow(1).Children(func() {
			a.treeDir(c, t, "")
		})
	})
}

// treeDir renders one directory's subtree; entries load on first expand
// and stay cached until 刷新.
func (a *App) treeDir(c *ui.Context, t *ui.Theme, dir string) {
	entries, ok := a.treeEntries[dir]
	if !ok {
		var err error
		entries, err = a.svc.WorkspaceTree(a.activeID, dir)
		if err != nil {
			a.treeEntries[dir] = nil
			ui.Text(c, "读取失败："+err.Error()).TextColor(t.Danger).FontSize(12)
			return
		}
		a.treeEntries[dir] = entries
	}
	for _, e := range entries {
		if e.IsDir {
			sub := joinRel(dir, e.Name)
			open := a.treeExpanded[sub]
			ui.TreeItem(c, e.Name, &open, func() {
				a.treeExpanded[sub] = open
				a.treeDir(c, t, sub)
			})
			continue
		}
		path := joinRel(dir, e.Name)
		if ui.Button(c, e.Name).Grow(1).Clicked() {
			a.openPreview(path)
		}
	}
}

// openPreview loads a file's content and shows the preview modal.
func (a *App) openPreview(path string) {
	content, err := a.svc.ReadWorkspaceFile(a.activeID, path)
	if err != nil {
		a.previewName, a.previewText = path, "无法预览："+err.Error()
	} else {
		a.previewName, a.previewText = path, content
	}
	a.previewOpen = true
}

// previewModal shows one file, read-only（monospace，可选中复制）.
func (a *App) previewModal(c *ui.Context, t *ui.Theme) {
	if !a.previewOpen {
		return
	}
	ui.Modal(c, &a.previewOpen, func() {
		ui.Column(c).Width(760).Gap(10).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.previewName).Bold().Font("monospace")
				if ui.Button(c, "关闭").Clicked() {
					a.previewOpen = false
				}
			})
			ui.Scroll(c).MaxHeight(480).Children(func() {
				ui.Text(c, a.previewText).Font("monospace").FontSize(12).
					TextColor(t.Text).Selectable()
			})
		})
	})
}

// joinRel joins a parent directory and a name into a workspace-relative
// path（斜杠分隔，根目录父为空串）.
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ToggleFiles opens or closes the file tree panel.
func (a *App) ToggleFiles() {
	a.filesOpen = !a.filesOpen
	if a.filesOpen && a.treeEntries == nil {
		a.treeEntries = map[string][]tools.DirEntry{}
		a.treeExpanded = map[string]bool{}
	}
}
