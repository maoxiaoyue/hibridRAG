# evals

獨立小工具（比照 `black_browser/` 的慣例，自己的 `go.mod`，不掛在任何服務底下），
用來跑品質評估（P2-3，見根目錄 [TODO4.md](../TODO4.md)）。目前只做 **Phase 0 + Phase 1**：

- **Phase 0**：eval dataset schema + loader + validate 工具 + 種子案例
- **Phase 1**：BLEU scorer + 最小 runner（呼叫 `agents` 產生答案 → 算 BLEU → 印表 + 存 JSON）

**LLM-as-judge（Phase 3）與人工標記（Phase 4）尚未實作**——見下方「後續 phase」。

## 目錄結構

```
evals/
  proto/agent.proto        # agents/proto/agent.proto 的副本（只改 go_package，見下方「為什麼複製 proto」）
  pb/agentpb/               # 由上面那份 proto 生成
  cases/*.yaml               # eval dataset（git 追蹤，見下方 schema）
  pkg/dataset/                # Case 型別 + LoadDir + Validate
  pkg/bleu/                    # 簡化版 BLEU-4（CJK 逐字 tokenize）
  pkg/agentclient/              # 對 agents 的最小 gRPC client（只用 Chat，單輪無工具）
  cmd/validate/                  # lint dataset
  cmd/run/                        # 跑 dataset，印總表 + 寫 results/*.json
```

## 用法

```bash
cd evals

# 1. 先驗證 dataset 格式（不需要任何服務在跑）
go run ./cmd/validate -cases cases

# 2. 對 agents 服務跑一輪（預設 127.0.0.1:9091，agents 預設自簽 TLS）
go run ./cmd/run -cases cases -addr 127.0.0.1:9091

# 常用旗標
go run ./cmd/run -agent demo -category rag_qa    # 只跑特定 agent_id / category
go run ./cmd/run -out results/2026-07-12.json    # 自訂輸出路徑
```

## Dataset schema

`cases/*.yaml` 每個檔案是一個案例陣列（不是巢狀 `cases:` key）：

```yaml
- id: go-001                    # 全域唯一，validate 會擋重複
  agent_id: demo                # 對應 agents/skills/{agent_id}/SKILL.md
  category: general_chat        # general_chat | rag_qa（rag_qa 需要 context_sources）
  tags: [go, defer]
  input:
    question: "..."
    context_sources:            # 選填；category=rag_qa 時必填
      - "..."
  expected:
    answer: "..."                # BLEU 的參考句；至少要有 answer 或 key_points 其中之一
    key_points: ["...", "..."]   # 留給 Phase 3 LLM-as-judge 用
  difficulty: easy               # easy | medium | hard（選填）
  notes: "..."                    # 選填，例如 "regression: bug #123"
```

新增案例：找對應 category 的檔案加一筆（或開新檔案，`LoadDir` 會掃整個 `cases/` 目錄），
跑 `cmd/validate` 確認格式正確即可，不需要碰任何程式碼或 DB migration。

## 為什麼複製 proto 而不是 import agents

`evals` 是獨立 module，不能直接 `import "agent/app/models"`（跨 module 沒有 replace/vendor）。
比照本 repo `gatewaypb` 在多個 client module 間的做法：**各自複製一份 proto + regenerate**，
不共用同一份 generated code。`agents/proto/agent.proto` 的 `Chat`/`ChatTurnReq`/`ChatTurnResp`
契約變更時，這份要手動同步：

```bash
cd evals
protoc --proto_path=proto --go_out=. --go_opt=module=evals \
  --go-grpc_out=. --go-grpc_opt=module=evals proto/agent.proto
```

## 已知限制（Phase 0/1 範圍內，非 bug）

- **只支援單輪、無 client tools 的問答**。案例若讓 agent 觸發 `status=tool_calls`，
  `agentclient.Ask` 會回傳 `ErrToolCallsUnsupported`（該案例的 `error` 欄位會顯示這個訊息）——
  代表這個案例目前不適合拿來評分，不是程式錯誤。
- BLEU 只在 `expected.answer` 非空的案例上算；只給 `key_points` 的案例目前會被標記 `skipped`
  （留給 Phase 3 的 LLM-as-judge，因為「是否涵蓋這些要點」比字面比對更適合語意評分）。
- **本次實作未跑 `go build`/`go vet`/`go test`/實際執行**（依需求「先實作，不驗證」）——
  下一步應先跑一輪驗證再依賴這份程式碼。

## 後續 phase（尚未實作）

- **Phase 2**：`agent_critic` 把 `Scorer` 抽成正式介面（`Score(ctx, question, answer, sources) (ScoreResult, error)`），
  `LexicalScorer` 保留當預設實作，機械式重構、零行為變更。
- **Phase 3**：`agent_critic` 新增 `LLMJudgeScorer`（獨立 Ollama client，`format:"json"` 結構化評分，
  評分模型與被測模型不同顆避免自我偏好）；`evals/cmd/run` 改為（或並行）呼叫 `agent_critic.Evaluate`
  而不只是本地 BLEU；`config.critic.scorer_mode: lexical | llm_judge | both`。
- **Phase 4**：人工標記——`critic_evaluations` 加 nullable `human_*` 欄位，CSV 匯出/匯入 round-trip
  （不做審核 UI，量大再考慮）。
