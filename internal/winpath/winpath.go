// Package winpath 把 Windows 路径规范化成长形态——8.3 短名（如
// RUNNER~1）经 GetLongPathName 展开回 runneradmin。git 的 toplevel 输
// 出长形态，而 Go 的 TempDir 沿用 TMP 环境变量里的短名形态，两侧字符
// 串比较会把同一个目录错判成两个（CI windows runner 实测：检查点判定
// "not a git repository"、层级上下文静默降级单层）。非 Windows 恒等。
package winpath

// Long returns p in long form. 失败（不存在/无效/无权限）原样返回——
// 调用方把结果用于比较而非访问，坏输入退化为旧行为。
func Long(p string) string { return long(p) }
