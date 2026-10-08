// Package dataset 定義 eval 案例的 schema，並從 YAML 檔載入。
//
// 案例存在 git 追蹤的 YAML 檔（不進 DB）：可在 PR 裡 diff/review、新增案例
// 不需要任何服務跑起來、不需要 DB migration。每個檔案是一個 Case 陣列
// （用類別分檔，例如 cases/go_basics.yaml、cases/rag_concepts.yaml）。
package dataset

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Case 一筆 eval 案例。
type Case struct {
	ID         string   `yaml:"id"`
	AgentID    string   `yaml:"agent_id"`
	Category   string   `yaml:"category"` // 例 "general_chat" | "rag_qa"
	Tags       []string `yaml:"tags,omitempty"`
	Input      Input    `yaml:"input"`
	Expected   Expected `yaml:"expected"`
	Difficulty string   `yaml:"difficulty,omitempty"` // "easy" | "medium" | "hard"
	Notes      string   `yaml:"notes,omitempty"`

	// SourceFile 不從 YAML 讀，Load 時填入，供錯誤訊息/除錯定位用。
	SourceFile string `yaml:"-"`
}

// Input 送給 agent 的問題與（選填）已知上下文。
type Input struct {
	Question string `yaml:"question"`
	// ContextSources 選填：案例已內附的證據片段，供不需要即時檢索、
	// 只想測「模型是否忠於給定 sources」的案例使用（category=rag_qa 時必填）。
	ContextSources []string `yaml:"context_sources,omitempty"`
}

// Expected 案例的預期輸出。Answer 與 KeyPoints 至少要有一個非空
//（Answer 給 BLEU 用；KeyPoints 給日後 LLM-as-judge 用，見 evals 計畫 Phase 3）。
type Expected struct {
	Answer         string   `yaml:"answer,omitempty"`
	KeyPoints      []string `yaml:"key_points,omitempty"`
	MustNotContain []string `yaml:"must_not_contain,omitempty"`
}

// LoadDir 讀取 dir 底下所有 *.yaml / *.yml 檔（不遞迴子目錄），合併成一份
// 案例清單，依 ID 排序後回傳。單一檔案格式錯誤會讓整個 LoadDir 失敗
// （寧可整批擋下也不要漏掉壞掉的案例悄悄跑過）。
func LoadDir(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dataset dir %s: %w", dir, err)
	}

	var all []Case
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		cases, err := loadFile(path)
		if err != nil {
			return nil, err
		}
		all = append(all, cases...)
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

func loadFile(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cases []Case
	if err := yaml.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range cases {
		cases[i].SourceFile = path
	}
	return cases, nil
}
