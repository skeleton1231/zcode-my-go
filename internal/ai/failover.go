package ai

// Failover 是模型兜底链（spec 规则 55）：主模型流以可重试错误收尾时
// 依次换 fallbackModels 重发同一请求——429/过载的常见出路是降级模
// 型，而非让整轮失败。非可重试错误（鉴权/参数类）换模型无用，原样
// 透传；全部耗尽返回末次错误（保持 Retryable，交上层整链重试）。
// 同一 provider 客户端复用——模型是请求级字段，逐次尝试改写。

import "context"

// NewFailover 把单个 provider 包成按模型序尝试的兜底链：models[0] 是
// 主模型（通常与 req.Model 相同），其后的按序兜底。空/单元素链即原
// provider 行为。
func NewFailover(p Provider, models []string) Provider {
	if len(models) <= 1 {
		return p
	}
	return &failover{p: p, models: models}
}

type failover struct {
	p      Provider
	models []string
}

// Stream implements Provider：逐模型尝试，中继首个成功流；可重试错误
// 换下一模型，非可重试错误透传终止。
func (f *failover) Stream(ctx context.Context, req Request) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		var lastRetryable StreamEvent
		for _, model := range f.models {
			attempt := req
			attempt.Model = model
			failed := false
			for ev := range f.p.Stream(ctx, attempt) {
				if ev.Kind == EvMessageEnd && ev.StopReason == StopError {
					if !ev.Retryable {
						out <- ev // 鉴权/参数类：换模型无用，原样透传
						return
					}
					lastRetryable = ev
					failed = true
					break
				}
				out <- ev
			}
			if !failed {
				return // 本模型成功（正常 MessageEnd/中止），兜底链完成
			}
		}
		// 全部模型耗尽：透传末次可重试错误（保持可重试，交上层整链再试）。
		out <- lastRetryable
	}()
	return out
}
