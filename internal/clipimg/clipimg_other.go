//go:build !windows

package clipimg

// ReadImage 在非 Windows 平台一律无图（贴图入口只对 Windows 一等公民
// 实现；其余平台 Ctrl+V 走纯文本粘贴，行为不变）。
func ReadImage() ([]byte, error) { return nil, ErrNoImage }
