package service

// 模型目录在线刷新（spec 规则 125）：zgo models --refresh 按活动剖
// 面拉模型清单，合并进 dataDir/models.json 覆盖层；启动时载入参与
// LookupModel/KnownModel（内置条目优先——覆盖层只补未知 id）。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/skeleton1231/zcode-my-go/internal/ai"
)

// modelsOverlayFile 是覆盖层的持久化形态（dataDir/models.json）。
type modelsOverlayFile struct {
	Models    []ai.ModelInfo `json:"models"`
	FetchedAt time.Time      `json:"fetchedAt"`
}

// loadModelOverlay 启动时载入覆盖层（缺文件/损坏 = 静默空层——覆盖层
// 是增益面，不能阻塞启动；损坏时下次 refresh 会重写）。
func (s *AgentService) loadModelOverlay() {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "models.json"))
	if err != nil {
		return
	}
	var f modelsOverlayFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	ai.ApplyCatalogOverlay(f.Models)
}

// RefreshModels 拉活动剖面端点的模型清单并落覆盖层（zgo models
// --refresh）。返回（拉到条数, 新进覆盖层的条数）。清单里与内置目录
// 同名的 id 不进覆盖层——内置条目带牌价/窗口，优先级更高。
func (s *AgentService) RefreshModels(ctx context.Context) (fetched, added int, err error) {
	cfg := s.resolve()
	if cfg.APIKey == "" && cfg.OAuth.AccessToken == "" {
		return 0, 0, errNoProvider
	}
	list, err := ai.FetchModelList(ctx, cfg.API, cfg.BaseURL, cfg.APIKey, nil)
	if err != nil {
		return 0, 0, err
	}
	fetched = len(list)
	var keep []ai.ModelInfo
	for _, m := range list {
		if ai.KnownModel(m.ID) && !overlayContainsID(ai.CatalogOverlay(), m.ID) {
			continue // 内置已有（且非既有覆盖层条目）：保留内置版
		}
		keep = append(keep, m)
	}
	added = len(keep)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fetched, 0, err
	}
	f := modelsOverlayFile{Models: keep, FetchedAt: time.Now()}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fetched, added, err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.path), "models.json"), data, 0o644); err != nil {
		return fetched, added, err
	}
	ai.ApplyCatalogOverlay(keep)
	return fetched, added, nil
}

// overlayContainsID 判重（刷新覆盖既有覆盖层时保留旧标记语义——同 id
// 已在覆盖层则延续，避免内置判断把它吸走）。
func overlayContainsID(overlay []ai.ModelInfo, id string) bool {
	for _, m := range overlay {
		if m.ID == id {
			return true
		}
	}
	return false
}
