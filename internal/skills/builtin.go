package skills

// 内置技能层（spec 规则 72，ZCode bundled-skills 的 Zgo 形态）：随二
// 进制 go:embed 发行的高价值技能（skill-creator / go-testing /
// debug-methodically），优先级最低——工作区与全局层同名即覆盖。嵌入
// FS 走与本地/远端同一 Root 面，发现/加载/注入零特判。
//
// 内置层不是注入面：内容随产品发行、不读磁盘——未授信工作区同样
// 可用（信任门只挡工作区层）。

import (
	"embed"
	"path"
)

//go:embed builtin
var builtinEmbed embed.FS

// ScopeBuiltin 标记内置层（展示与优先级判定用）。
const ScopeBuiltin Scope = "builtin"

// embeddedFS 把 embed.FS 适配成技能根的 FS 面（路径一律斜杠形态）。
type embeddedFS struct{}

// Join implements FS.
func (embeddedFS) Join(parts ...string) string { return path.Join(parts...) }

// ReadFile implements FS.
func (embeddedFS) ReadFile(name string) ([]byte, error) { return builtinEmbed.ReadFile(name) }

// ReadDir implements FS.
func (embeddedFS) ReadDir(dir string) ([]DirEntry, error) {
	des, err := builtinEmbed.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]DirEntry, len(des))
	for i, de := range des {
		out[i] = DirEntry{Name: de.Name(), IsDir: de.IsDir()}
	}
	return out, nil
}

// BuiltinRoot 返回内置技能根（放 roots 末位——先到先得的优先级语
// 义下即最低优先，同名被上层覆盖）。
func BuiltinRoot() Root {
	return Root{Base: "builtin", Scope: ScopeBuiltin, FS: embeddedFS{}}
}
