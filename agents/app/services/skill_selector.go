package services

import (
	"sort"
	"strings"
	"unicode"

	"agent/app/models"
)

// SkillSelector 依 query 從 SkillPack.References 動態挑選相關片段
//
// 初版：輕量詞頻 + 標題 / keyword 權重
//   - title hit:    ×3
//   - keyword hit:  ×2
//   - body hit:     ×1
//
// 未來可替換成向量檢索（embedding + cosine），介面 Select 不變。
type SkillSelector struct {
	loader *SkillLoader
}

func NewSkillSelector(loader *SkillLoader) *SkillSelector {
	return &SkillSelector{loader: loader}
}

// Select 依 query 挑選 references，受 maxDocs + maxTokens 雙重預算限制
// maxDocs <= 0 視為無上限；maxTokens <= 0 視為無上限
func (s *SkillSelector) Select(agentID, query string, maxDocs, maxTokens int) ([]models.PickedRef, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	pack, err := s.loader.Get(agentID)
	if err != nil {
		return nil, err
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}

	type scored struct {
		ref   *models.Reference
		score float64
	}

	ranked := make([]scored, 0, len(pack.References))
	for _, ref := range pack.References {
		sc := scoreReference(ref, terms)
		if sc > 0 {
			ranked = append(ranked, scored{ref: ref, score: sc})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].ref.Path < ranked[j].ref.Path
	})

	out := make([]models.PickedRef, 0, len(ranked))
	tokenBudget := maxTokens
	for _, r := range ranked {
		if maxDocs > 0 && len(out) >= maxDocs {
			break
		}
		if maxTokens > 0 {
			if tokenBudget <= 0 {
				break
			}
			if r.ref.Tokens > tokenBudget && len(out) > 0 {
				// 已放入至少一份且下一份超出預算，結束
				break
			}
			tokenBudget -= r.ref.Tokens
		}
		out = append(out, models.PickedRef{
			Path:  r.ref.Path,
			Title: r.ref.Title,
			Score: r.score,
			Body:  r.ref.Body,
		})
	}
	return out, nil
}

// ---- 內部 helper ----

// scoreReference 用三段計分：title / keywords / body
func scoreReference(ref *models.Reference, terms []string) float64 {
	titleLower := strings.ToLower(ref.Title)
	bodyLower := strings.ToLower(ref.Body)

	keywordSet := make(map[string]struct{}, len(ref.Keywords))
	for _, k := range ref.Keywords {
		keywordSet[strings.ToLower(k)] = struct{}{}
	}

	var score float64
	for _, t := range terms {
		if t == "" {
			continue
		}
		if strings.Contains(titleLower, t) {
			score += 3
		}
		for kw := range keywordSet {
			if strings.Contains(kw, t) {
				score += 2
				break
			}
		}
		if c := strings.Count(bodyLower, t); c > 0 {
			// body 採 log-ish：避免超長文件灌水
			if c > 5 {
				c = 5
			}
			score += float64(c)
		}
	}
	return score
}

// tokenize 簡易分詞：英文依非字母數字切；中文每字成 bigram（與單字都收）
func tokenize(s string) []string {
	s = strings.ToLower(s)
	seen := make(map[string]struct{})
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if len(t) < 2 {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}

	var ascii strings.Builder
	var cjk []rune
	flushAscii := func() {
		if ascii.Len() > 0 {
			add(ascii.String())
			ascii.Reset()
		}
	}
	flushCJK := func() {
		for i := 0; i < len(cjk); i++ {
			add(string(cjk[i]))
			if i+1 < len(cjk) {
				add(string(cjk[i : i+2])) // bigram
			}
		}
		cjk = cjk[:0]
	}

	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flushAscii()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			ascii.WriteRune(r)
		default:
			flushAscii()
			flushCJK()
		}
	}
	flushAscii()
	flushCJK()
	return out
}
