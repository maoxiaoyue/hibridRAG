# agent_gateway — A2A Gateway 服務

> 模組名稱：`agent_gatway`（注意：原始 scaffold 有拼寫錯誤，保持不變以免破壞現有引用）
> 監聽埠：`:8090`（預設）
> 設定檔：`config/config.yaml`

---

## 功能概述

`agent_gateway` 是整個 A2A 多 Agent 架構的**唯一對外入口**。

客戶端不需要知道後端有哪些 Agent、部署在哪裡。只要把符合 A2A 協議的 JSON-RPC 請求打到 gateway，gateway 會：

1. 解析請求中的 `x-skill` 欄位（技能提示）
2. 向 `agent_registry`（`:8500`）查詢最適合的 Agent
3. 把原始 JSON-RPC 請求轉發給該 Agent
4. 把 Agent 的回應原封不動地回傳給客戶端

Gateway 本身**無狀態**——不儲存任何任務記錄，task_id 由 gateway 產生後注入並傳給下游 Agent 管理。

---

## API 端點

### `GET /.well-known/agent-card.json`

回傳 gateway 自身的 A2A Agent Card（符合 Google ADK 規範）。

```json
{
  "name": "A2A Gateway",
  "description": "Central A2A entry point — routes requests to registered agents via skill discovery",
  "version": "1.0.0",
  "url": "https://localhost:8090/",
  "capabilities": {
    "streaming": false,
    "push_notifications": false
  },
  "skills": [
    {
      "id": "gateway",
      "name": "Gateway Routing",
      "description": "Routes A2A messages to the appropriate downstream agent based on skill discovery"
    }
  ]
}
```

---

### `POST /`

A2A JSON-RPC 2.0 入口。接受以下 method：

#### `message/send`（已實作）

把訊息路由給下游 Agent。

**請求範例：**
```json
{
  "jsonrpc": "2.0",
  "id": "req-1",
  "method": "message/send",
  "params": {
    "x-skill": "majiang",
    "message": {
      "role": "user",
      "parts": [{ "text": "發牌" }]
    }
  }
}
```

| 欄位 | 說明 |
|------|------|
| `x-skill` | 技能提示（可選）。空白時回傳 registry 中第一個 Agent |
| `task_id` | 任務 ID（可選）。未提供時由 gateway 自動產生 `task_<8-byte-hex>` |

**成功回應：** 下游 Agent 的 JSON-RPC response 原樣轉發。

**錯誤回應：**
| code | 意義 |
|------|------|
| `-32700` | 請求無法解析（parse error） |
| `-32600` | jsonrpc 版本不是 `"2.0"` |
| `-32601` | method 不支援（gateway 層） |
| `-32001` | registry 找不到符合技能的 Agent |
| `-32002` | 下游 Agent 呼叫失敗（連線錯誤） |

#### `tasks/get`、`tasks/cancel`（保留，未實作）

目前回傳 `-32601 method not supported at gateway level`。任務狀態由各 Agent 自行管理。

---

## 路由流程

```
Client
  │  POST / {jsonrpc, method: "message/send", params: {x-skill, message}}
  ▼
a2a_handler.go (A2ADispatcher)
  │  解析 JSON-RPC，提取 x-skill
  ▼
registry_client.go (SearchBySkill)
  │  GET https://registry:8500/registry/search?skill=<x-skill>
  │  registry 端 Search() 先做「skill id 精確比對」，找不到才 fallback 做子字串模糊比對
  │  （避免舊測試殘留 agent 的 skill 因子字串相符而搶到路由）
  │  回傳 last_seen_at DESC 排序的第一筆 Agent；若同時有多個不同 agent 符合會記 warning log
  ▼
a2a_client.go (Forward)
  │  POST <agent.url>  {原始 JSON-RPC + task_id}
  │  timeout: 30s（可設定）
  ▼
Client ← JSON-RPC response
```

---

## 目錄結構

```
agent_gateway/
├── main.go                        # 入口：呼叫 commands.Execute()
├── config/
│   └── config.yaml                # 服務設定
├── app/
│   ├── commands/
│   │   └── root.go                # Cobra root + serve 子命令（啟動 HTTP server）
│   ├── config/
│   │   └── config.go              # Config struct + YAML loader
│   ├── models/
│   │   └── a2a.go                 # A2A 協議型別定義
│   ├── controllers/
│   │   └── a2a_handler.go         # AgentCardHandler + A2ADispatcher
│   ├── services/
│   │   ├── agent_card.go          # 從 config 組建 AgentCard
│   │   ├── registry_client.go     # HTTP client → agent_registry
│   │   └── a2a_client.go          # 轉發 JSON-RPC 給下游 Agent
│   └── routers/
│       └── routes.go              # 路由註冊
```

---

## 設定檔說明 (`config/config.yaml`)

```yaml
server:
  protocol: http2      # HTTP/2 (h2, TLS + ALPN)
  addr: :8090          # Gateway 監聽埠
  tls:
    enabled: true
    cert_file: "certs/server.crt"
    key_file: "certs/server.key"

registry:
  url: "https://localhost:8500"   # agent_registry 位址

gateway:
  name: "A2A Gateway"
  description: "..."
  version: "1.0.0"
  public_url: "https://localhost:8090"   # 對外 URL（寫入 agent-card）
  downstream_timeout_sec: 30            # 下游 Agent 呼叫 timeout（秒）

logger:
  level: info
  output: stdout
  colors: true
```

---

## 啟動方式

```bash
# 需先啟動 agent_registry
cd D:\GoProjects\agent_registry && go run . serve

# 再啟動 gateway
cd D:\GoProjects\single_agent\agent_gateway && go run . serve

# 或編譯後執行
go build -o agent_gateway . && ./agent_gateway serve
```

---

## 依賴服務

| 服務 | 位址 | 必要性 |
|------|------|--------|
| `agent_registry` | `:8500`（可設定） | 必要——gateway 無法路由沒有 registry |

Gateway **不直接存取** PostgreSQL、Redis、Cassandra；所有持久化由 registry 和各 Agent 負責。

---

## gRPC（AgentService/Inbound，`:9090`）

除了 HTTP/A2A 入口，gateway 同時起一個 gRPC server（`grpc_handler.go` 的 `GRPCGatewayServer`），接收上游呼叫並依 `payload.agent_id`（找不到則 fallback 用 skill 模糊搜尋）查 registry 解析下游 gRPC endpoint，再透過 `grpc_forwarder.go` 轉發。

- gRPC server 與 HTTP server 共用同一組自簽憑證（`certs/server.crt`+`server.key`，`server.tls.enabled: true` 時生效；未設定則退回明文並記 warning log）
- `grpc_forwarder.go` 撥往下游（如 `agents` 的 `:9091`）一律走 TLS + `InsecureSkipVerify`（自簽憑證，dev 用），與 `a2a_client.go`/`registry_client.go` 的慣例一致

---

## ClientGateway.ChatStatus（對話階段輪詢，2026-07-07）

`ClientGateway`（dev_pub_0.9 的 client-facing gRPC，`proto/client_gateway.proto`）新增
`rpc ChatStatus(ChatStatusReq) returns (ChatStatusResp)`：client 對話中每秒輪詢「目前哪個 Agent
在處理」，gateway **走內部 HTTP** 打 agents 的 `GET /api/chat/status/:session_id`（不占 gRPC，reuse
`s.httpClient`），把 `{stage, active}` 鏡像回 client。輪詢失敗一律回空狀態，不擾動對話。

- config 新增 `internal.agents_http_base`（例 `https://localhost:18080`）；留空 → ChatStatus 回空狀態。
- 重生 pb：`D:/GoProjects/bin/protoc.exe --proto_path=proto --go_out=. --go_opt=module=agent_gatway
  --go-grpc_out=. --go-grpc_opt=module=agent_gatway proto/client_gateway.proto`，再把
  `pb/gatewaypb/*.pb.go` 複製到 `agent_client/pb/gatewaypb/` 與 `pub_client/pb/gatewaypb/`。
- 階段標籤由 agents 端提供（`Agent 1 · ContextBuilder` / `Agent 2 · ToolLoop` / `Agent 4 · Graph` …）。

`ClientGateway` 另有 `rpc ChatImage(ChatImageReq) returns (ChatImageResp)`（2026-07-07）：對話附加
圖片辨識,gateway 內部 HTTP POST 原始圖片 bytes 到 agents `POST /api/chat/image`（專屬 120s client,
reuse TLS transport;vision 抽文字較久）,回傳抽出的文字。client 把文字併進該輪訊息。重生 pb 方式同上。

## ClientGateway.VaultManifest / VaultFetch（Sync Vault，2026-07-08）

轉發 agent_graph 的同名 RPC：client「Sync Vault」把 server 端 Obsidian vault 的 .md 鏡像到本地。
`VaultFetch` 是 **repo 第一個 server-streaming RPC**——forwarder 下游 `Recv()` → 上游 `Send()`
邊收邊轉（UploadIngest 的相反方向），錯誤原樣透傳下游 gRPC status。stream auth interceptor 按
`ClientGateway` prefix 攔，新 RPC 自動涵蓋（interceptor 零改動）。`GraphEntity` 同日加 `degree`
欄位（=6，agent_graph 回填的全圖關係數）。重生 pb 方式同上節；記得同步複製到
`pub_client/pb/gatewaypb/` 與 `rag_admin/pb/gatewaypb/`。

## 擴充方向（TODO3.md Phase 2/3）

- `tasks/subscribe`：SSE 串流支援
- 認證 middleware（Bearer token）
- 多 Agent 負載均衡（目前只選第一筆）
- Circuit breaker / retry

---

## 修正紀錄

已遷移至專案 [memory/20260703_log.md](../memory/20260703_log.md)（「一、agent_gateway 路由修正」「二、全服務 HTTP/2 + 真 TLS」「三、gRPC 補 TLS」節）。
