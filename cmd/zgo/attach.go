package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
	"github.com/skeleton1231/zcode-my-go/internal/service"
)

// attachCapBytes bounds one attached image（与桌面 📎 同限）.
const attachCapBytes = 5 << 20

// attachTotalCapBytes bounds one message's pending images（编码后口径，
// 与桌面 attachmentTotalCapBytes 及 session 层 maxImageTotal 兜底同限：
// 挂载时提前拒绝并提示分条——而不是发送时才被兜底静默少发，issue #132）.
const attachTotalCapBytes = 6 << 20

// loadImageBlocks turns image paths into message blocks; failures come
// back as human-readable errors instead of aborting the whole send.
func loadImageBlocks(paths []string) (blocks []ai.Block, errs []string) {
	for _, p := range paths {
		mt := service.ImageMediaType(p)
		if mt == "" {
			errs = append(errs, fmt.Sprintf("%s：不是支持的图片类型", p))
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s：%v", p, err))
			continue
		}
		if len(data) > attachCapBytes {
			errs = append(errs, fmt.Sprintf("%s：超过 %dMB 上限", p, attachCapBytes>>20))
			continue
		}
		blocks = append(blocks, ai.Block{Kind: ai.BlockImage, Media: mt,
			Data: base64.StdEncoding.EncodeToString(data)})
	}
	return blocks, errs
}

// attachments is the terminal surfaces' pending-attachment state
// （/attach 攒图，随下一条消息发送后清空）.
type attachments struct {
	pending []ai.Block
	names   []string
}

// add validates and queues one image; the returned line is the user
// feedback. 总量按编码字节数提前拒绝（与桌面同文案口径）。
func (a *attachments) add(path string) string {
	blocks, errs := loadImageBlocks([]string{path})
	if len(errs) > 0 {
		return errs[0]
	}
	blk := blocks[0]
	if a.totalBytes()+len(blk.Data) > attachTotalCapBytes {
		return fmt.Sprintf("附件总量超过 %dMB 上限，请分条发送：%s",
			attachTotalCapBytes>>20, path)
	}
	a.pending = append(a.pending, blk)
	a.names = append(a.names, path)
	return fmt.Sprintf("已附 %s（发送下一条消息时随行，共 %d 张）", path, len(a.pending))
}

// totalBytes 报告待发附件的编码总量（base64 字节数，与兜底口径一致）。
func (a *attachments) totalBytes() int {
	n := 0
	for _, b := range a.pending {
		n += len(b.Data)
	}
	return n
}

// take drains the queue for sending.
func (a *attachments) take() []ai.Block {
	b := a.pending
	a.pending, a.names = nil, nil
	return b
}

// status is the pending indicator for prompt/status lines.
func (a *attachments) status() string {
	if len(a.pending) == 0 {
		return ""
	}
	return fmt.Sprintf("📎×%d", len(a.pending))
}
