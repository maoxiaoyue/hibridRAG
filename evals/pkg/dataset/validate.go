package dataset

import (
	"fmt"
	"strings"
)

// Validate 對已載入的案例做基本 lint：格式錯誤在這裡擋下，而不是讓
// runner 跑到一半才因為缺欄位而困惑地失敗。回傳空 slice 代表全部通過。
func Validate(cases []Case) []error {
	var errs []error
	seenID := map[string]string{} // id → 第一次出現的 SourceFile

	for _, c := range cases {
		loc := fmt.Sprintf("%s (id=%q)", c.SourceFile, c.ID)

		if strings.TrimSpace(c.ID) == "" {
			errs = append(errs, fmt.Errorf("%s: missing id", c.SourceFile))
			continue // 沒有 id 就沒辦法做後續的重複檢查與有意義的錯誤定位
		}
		if prev, dup := seenID[c.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id %q (first seen in %s)", c.SourceFile, c.ID, prev))
		}
		seenID[c.ID] = c.SourceFile

		if strings.TrimSpace(c.AgentID) == "" {
			errs = append(errs, fmt.Errorf("%s: missing agent_id", loc))
		}
		if strings.TrimSpace(c.Category) == "" {
			errs = append(errs, fmt.Errorf("%s: missing category", loc))
		}
		if strings.TrimSpace(c.Input.Question) == "" {
			errs = append(errs, fmt.Errorf("%s: missing input.question", loc))
		}
		if c.Category == "rag_qa" && len(c.Input.ContextSources) == 0 {
			errs = append(errs, fmt.Errorf("%s: category=rag_qa requires at least one input.context_sources entry", loc))
		}
		if strings.TrimSpace(c.Expected.Answer) == "" && len(c.Expected.KeyPoints) == 0 {
			errs = append(errs, fmt.Errorf("%s: expected.answer and expected.key_points are both empty (need at least one)", loc))
		}
		if c.Difficulty != "" && c.Difficulty != "easy" && c.Difficulty != "medium" && c.Difficulty != "hard" {
			errs = append(errs, fmt.Errorf("%s: invalid difficulty %q (want easy|medium|hard)", loc, c.Difficulty))
		}
	}
	return errs
}
