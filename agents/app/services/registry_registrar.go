// registry_registrar.go — 啟動時自動向 agent_registry 註冊 Agent Card + 心跳。
//
// 背景：registry 的 Search() 同分時依 last_seen_at DESC 排序，且 gateway 的
// skill→agent 路由完全依賴 registry 的登錄資料——沒有自動註冊的話，多實例模式
// （同一 binary、不同 config = 不同 agent）每次部署都要手動 POST /registry/register。
//
// 行為：
//   - Start()：立即註冊一次（失敗只 log，不阻擋服務啟動），之後每 HeartbeatSec
//     重新註冊（registry 端 Upsert 語意，等同刷新 last_seen_at）。
//   - 卡片每次心跳都重建（SkillPackSource.Load 帶 mtime 快取，SKILL.md 沒改時
//     幾乎零成本；改了會自動反映到 registry）。
//   - Stop()：停心跳，並 best-effort DELETE /registry/agents/{id} 反註冊。
package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"agent/app/models"
	"agent/internal/agent/persona"
)

// SkillPackSource registrar 需要的 skill 載入能力；*SkillLoader 與
// *CompilingSkillLoader 都滿足（後者的 Load 會順帶編譯 ROLE.md/model 偏好）。
type SkillPackSource interface {
	Load(agentID string, force bool) (*models.SkillPack, bool, []string, error)
}

// RegistrarOptions RegistryRegistrar 的建構參數。
type RegistrarOptions struct {
	// RegistryURL agent_registry base URL（例 "https://localhost:8500"）；必填
	RegistryURL string
	// AgentID 本 agent 識別子（config agent.id）
	AgentID string
	// PublicURL 註冊 payload 的 url 欄位；留空 → 用 AgentCardBuilder 卡片的 URL
	PublicURL string
	// GRPCEndpoint 對外 gRPC endpoint（host:port）；空表示不支援 gRPC 直連
	GRPCEndpoint string
	// HeartbeatSec 心跳間隔秒數；<=0 → 60
	HeartbeatSec int
	// InsecureSkipVerify 自簽憑證（dev）時跳過驗證，比照 gateway 的 RegistryClient
	InsecureSkipVerify bool
}

// RegistryRegistrar 向 agent_registry 註冊本 agent 並維持心跳。
type RegistryRegistrar struct {
	opts    RegistrarOptions
	builder *AgentCardBuilder
	skills  SkillPackSource
	http    *http.Client
	stop    chan struct{}
	done    chan struct{}
}

// registerAgentPayload 對應 agent_registry 的 models.RegisterAgentRequest。
type registerAgentPayload struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	URL          string                 `json:"url"`
	GRPCEndpoint string                 `json:"grpc_endpoint,omitempty"`
	Version      string                 `json:"version,omitempty"`
	Skills       []models.A2ASkill      `json:"skills"`
	Capabilities models.A2ACapabilities `json:"capabilities"`
}

// NewRegistryRegistrar 建立 registrar；RegistryURL 為空時回 nil（呼叫端據此停用）。
func NewRegistryRegistrar(opts RegistrarOptions, builder *AgentCardBuilder, skills SkillPackSource) *RegistryRegistrar {
	if opts.RegistryURL == "" {
		return nil
	}
	if opts.HeartbeatSec <= 0 {
		opts.HeartbeatSec = 60
	}
	transport := &http.Transport{}
	if opts.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 自簽憑證 dev 環境
	}
	return &RegistryRegistrar{
		opts:    opts,
		builder: builder,
		skills:  skills,
		http:    &http.Client{Timeout: 5 * time.Second, Transport: transport},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// buildCard 重建 Agent Card。Load(force=false) 帶 mtime 快取：SKILL.md 沒改時
// 只做 stat；有改會重新解析（CompilingSkillLoader 並順帶重編 ROLE.md/model 偏好）。
func (r *RegistryRegistrar) buildCard() models.AgentCard {
	var parsed *persona.ParseResult
	if r.skills != nil {
		if pack, _, _, err := r.skills.Load(r.opts.AgentID, false); err == nil && pack != nil {
			parsed = persona.Parse(pack.Body)
		}
	}
	if r.builder != nil {
		return r.builder.Build(r.opts.AgentID, parsed)
	}
	return models.AgentCard{Name: r.opts.AgentID}
}

// RegisterOnce 註冊（registry 端為 Upsert，重複呼叫等同刷新 last_seen_at）。
func (r *RegistryRegistrar) RegisterOnce(ctx context.Context) error {
	card := r.buildCard()
	url := r.opts.PublicURL
	if url == "" {
		url = card.URL
	}
	payload := registerAgentPayload{
		ID:           r.opts.AgentID,
		Name:         card.Name,
		Description:  card.Description,
		URL:          url,
		GRPCEndpoint: r.opts.GRPCEndpoint,
		Version:      card.Version,
		Skills:       card.Skills,
		Capabilities: card.Capabilities,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal register payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.opts.RegistryURL+"/registry/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("registry register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("registry register returned %d: %s", resp.StatusCode, b)
	}
	return nil
}

// Deregister best-effort 反註冊（DELETE /registry/agents/{id}）。
func (r *RegistryRegistrar) Deregister(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		r.opts.RegistryURL+"/registry/agents/"+r.opts.AgentID, nil)
	if err != nil {
		return err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("registry deregister: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("registry deregister returned %d: %s", resp.StatusCode, b)
	}
	return nil
}

// Start 立即註冊一次並啟動心跳 goroutine。註冊失敗只 log（registry 可能比本
// 服務晚起來），下一次心跳會重試。
func (r *RegistryRegistrar) Start() {
	go func() {
		defer close(r.done)

		attempt := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.RegisterOnce(ctx); err != nil {
				log.Printf("[registry] register agent=%s failed: %v (will retry on next heartbeat)",
					r.opts.AgentID, err)
			}
		}
		attempt()

		ticker := time.NewTicker(time.Duration(r.opts.HeartbeatSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				attempt()
			case <-r.stop:
				return
			}
		}
	}()
	log.Printf("[registry] auto-registration started: agent=%s registry=%s heartbeat=%ds",
		r.opts.AgentID, r.opts.RegistryURL, r.opts.HeartbeatSec)
}

// Stop 停止心跳並 best-effort 反註冊；供 graceful shutdown 呼叫。
func (r *RegistryRegistrar) Stop() {
	close(r.stop)
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Deregister(ctx); err != nil {
		log.Printf("[registry] deregister agent=%s failed: %v", r.opts.AgentID, err)
	}
}
