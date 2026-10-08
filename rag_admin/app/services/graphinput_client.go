// graphinput_client.go — 對 graphic_input 服務（:8505）的 REST client。
//
// 內網（dev_090）專用第二後端連線：知識圖譜人工輸入（文件雙路徑 + 結構化輸入）。
// Config.GraphInputBaseURL 留空 = 整個「Graph Input」分頁不建（外網 dev_pub_0.9
// 預設如此——外網 client 沒有直接寫圖譜的能力）。
package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rag_admin/app/models"
)

// GraphInputClient 連線到 graphic_input 服務。
type GraphInputClient struct {
	baseURL string
	actor   string
	http    *http.Client
}

// NewGraphInputClient baseURL 空時回傳 nil（呼叫端據此隱藏分頁）。
func NewGraphInputClient(baseURL string, insecure bool) *GraphInputClient {
	baseURL = normalizeHTTPBaseURL(baseURL)
	if baseURL == "" {
		return nil
	}
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — 內網自簽
	}
	return &GraphInputClient{
		baseURL: baseURL,
		actor:   localActor(),
		http:    &http.Client{Timeout: 300 * time.Second, Transport: tr}, // 上傳 + OCR/抽取可能較久
	}
}

// localActor hostname\username，寫進 graphic_input_log 的 actor 欄位。
func localActor() string {
	host, _ := os.Hostname()
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if host == "" {
		return name
	}
	if name == "" {
		return host
	}
	return host + "/" + name
}

type giEnvelope[T any] struct {
	Success bool   `json:"success"`
	Data    T      `json:"data"`
	Error   string `json:"error,omitempty"`
}

func giPost[TReq any, TResp any](ctx context.Context, c *GraphInputClient, path string, req TReq) (TResp, error) {
	var zero TResp
	body, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return zero, fmt.Errorf("call %s: %w", path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env giEnvelope[TResp]
	if err := json.Unmarshal(raw, &env); err != nil {
		return zero, fmt.Errorf("decode %s response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := env.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return zero, fmt.Errorf("%s status %d: %s", path, resp.StatusCode, msg)
	}
	return env.Data, nil
}

// UploadDocs 文件雙路徑上傳（multipart 串流，不整批載入記憶體）。
// targets: "obsidian" | "pg" | "both"。
func (c *GraphInputClient) UploadDocs(ctx context.Context, targets string, tags []string, filePaths []string) ([]models.GIDocResult, error) {
	for _, p := range filePaths {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var err error
		defer func() { pw.CloseWithError(err) }()
		if err = mw.WriteField("targets", targets); err != nil {
			return
		}
		if err = mw.WriteField("actor", c.actor); err != nil {
			return
		}
		if len(tags) > 0 {
			if err = mw.WriteField("tags", strings.Join(tags, ",")); err != nil {
				return
			}
		}
		for _, p := range filePaths {
			var f *os.File
			if f, err = os.Open(p); err != nil {
				return
			}
			var part io.Writer
			if part, err = mw.CreateFormFile("files", filepath.Base(p)); err != nil {
				f.Close()
				return
			}
			if _, err = io.Copy(part, f); err != nil {
				f.Close()
				return
			}
			f.Close()
		}
		err = mw.Close()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/input/upload", pr)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upload call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env giEnvelope[[]models.GIDocResult]
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode upload resp: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := env.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return nil, fmt.Errorf("upload status %d: %s", resp.StatusCode, msg)
	}
	return env.Data, nil
}

// UpsertEntity 結構化實體輸入。
func (c *GraphInputClient) UpsertEntity(ctx context.Context, entity models.GIEntityInput, force bool) (models.GIUpsertEntityResponse, error) {
	return giPost[models.GIUpsertEntityRequest, models.GIUpsertEntityResponse](ctx, c, "/api/input/entity",
		models.GIUpsertEntityRequest{Entity: entity, Force: force, Actor: c.actor})
}

// UpsertRelation 結構化關係輸入。
func (c *GraphInputClient) UpsertRelation(ctx context.Context, source, target, relType string) error {
	_, err := giPost[models.GIUpsertRelationRequest, json.RawMessage](ctx, c, "/api/input/relation",
		models.GIUpsertRelationRequest{Source: source, Target: target, Type: relType, Actor: c.actor})
	return err
}

// CheckDuplicates 重複偵測預查。
func (c *GraphInputClient) CheckDuplicates(ctx context.Context, name string) ([]models.GISimilarCandidate, error) {
	return giPost[models.GICheckRequest, []models.GISimilarCandidate](ctx, c, "/api/input/check",
		models.GICheckRequest{Name: name})
}

// BatchImportCSV CSV 批次匯入。kind: "entity" | "relation"。
func (c *GraphInputClient) BatchImportCSV(ctx context.Context, kind, csvText string) (models.GIBatchResponse, error) {
	return giPost[models.GIBatchRequest, models.GIBatchResponse](ctx, c, "/api/input/batch",
		models.GIBatchRequest{Actor: c.actor, CSVKind: kind, CSV: csvText})
}

// ListLog 輸入歷史。
func (c *GraphInputClient) ListLog(ctx context.Context, limit int) ([]models.GIInputLog, error) {
	url := c.baseURL + "/api/input/log?limit=" + strconv.Itoa(limit)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("log call: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env giEnvelope[[]models.GIInputLog]
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode log resp: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("log status %d: %s", resp.StatusCode, env.Error)
	}
	return env.Data, nil
}
