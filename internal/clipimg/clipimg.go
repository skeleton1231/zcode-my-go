package clipimg

import "errors"

// ErrNoImage 表示剪贴板上没有可用位图（无剪贴板、只有文本、或不支持
// 的格式）——调用方应放行为普通文本粘贴，不报错不吞文本。
var ErrNoImage = errors.New("clipboard has no image")
