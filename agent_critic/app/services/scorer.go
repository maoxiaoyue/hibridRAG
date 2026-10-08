package services

import (
	"fmt"
	"strings"
	"unicode"
)

// LexicalScorer 是不依賴 LLM 的基線評分器：以關鍵詞覆蓋率近似三個品質維度。
// 真正的語義級評估（NLI faithfulness、LLM-as-judge）之後以相同介面替換，
// 評分語意（0-1、越高越好）與 feedback 結構不變。
type LexicalScorer struct {
	// PassThreshold overall 達此分數（含）視為通過
	PassThreshold float64
	// WCompleteness / WGrounding / WFaithfulness 加權（Score 內自動正規化）
	WCompleteness float64
	WGrounding    float64
	WFaithfulness float64
}

// NewLexicalScorer 建立基線評分器；零值參數以預設值（0.7 / 0.4 / 0.3 / 0.3）補齊
func NewLexicalScorer(passThreshold, wCompleteness, wGrounding, wFaithfulness float64) *LexicalScorer {
	s := &LexicalScorer{
		PassThreshold: passThreshold,
		WCompleteness: wCompleteness,
		WGrounding:    wGrounding,
		WFaithfulness: wFaithfulness,
	}
	if s.PassThreshold <= 0 {
		s.PassThreshold = 0.7
	}
	if s.WCompleteness <= 0 && s.WGrounding <= 0 && s.WFaithfulness <= 0 {
		s.WCompleteness, s.WGrounding, s.WFaithfulness = 0.4, 0.3, 0.3
	}
	return s
}

// ScoreResult 三維評分 + 總分 + 修訂建議
type ScoreResult struct {
	Completeness float64
	Grounding    float64
	Faithfulness float64
	Overall      float64
	Passed       bool
	Feedback     []string
}

// Score 評估 answer 對 question 的品質：
//   - completeness：question 關鍵詞在 answer 中的覆蓋率
//   - grounding：answer 關鍵詞在 sources 中的支持率（無 sources 時為 0）
//   - faithfulness：answer 中「至少三成關鍵詞有 sources 支持」的句子比例
func (s *LexicalScorer) Score(question, answer string, sources []string) ScoreResult {
	qKeys := keywords(question)
	aKeys := keywords(answer)
	srcKeys := keywords(strings.Join(sources, "\n"))

	res := ScoreResult{}

	// completeness
	var missing []string
	res.Completeness, missing = coverage(qKeys, aKeys)
	if len(missing) > 0 {
		limit := len(missing)
		if limit > 5 {
			limit = 5
		}
		res.Feedback = append(res.Feedback,
			fmt.Sprintf("答案未涵蓋問題關鍵詞：%s", strings.Join(missing[:limit], "、")))
	}

	// grounding + faithfulness
	if len(sources) == 0 {
		res.Feedback = append(res.Feedback, "沒有提供任何 sources，無法驗證 grounding；請附上檢索到的證據片段")
	} else {
		res.Grounding, _ = coverage(aKeys, srcKeys)

		sentences := splitSentences(answer)
		supported := 0
		for _, sent := range sentences {
			ratio, _ := coverage(keywords(sent), srcKeys)
			if ratio >= 0.3 {
				supported++
			}
		}
		if len(sentences) > 0 {
			res.Faithfulness = float64(supported) / float64(len(sentences))
			if unsupported := len(sentences) - supported; unsupported > 0 {
				res.Feedback = append(res.Feedback,
					fmt.Sprintf("%d/%d 個句子缺乏 sources 支持，請補充引用或移除無依據的陳述", unsupported, len(sentences)))
			}
		}
	}

	wSum := s.WCompleteness + s.WGrounding + s.WFaithfulness
	res.Overall = (res.Completeness*s.WCompleteness + res.Grounding*s.WGrounding + res.Faithfulness*s.WFaithfulness) / wSum
	res.Passed = res.Overall >= s.PassThreshold
	return res
}

// asciiStopwords 疑問詞/功能詞不列入關鍵詞（避免每個問題自帶無法覆蓋的雜訊）
var asciiStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "was": {}, "were": {},
	"what": {}, "how": {}, "why": {}, "who": {}, "which": {}, "when": {}, "where": {},
	"does": {}, "did": {}, "can": {}, "could": {}, "should": {}, "would": {}, "will": {},
	"this": {}, "that": {}, "with": {}, "from": {}, "have": {}, "has": {}, "not": {},
	"you": {}, "your": {}, "please": {},
}

// cjkFunctional 中文功能字；bigram 兩字皆屬此集合時視為雜訊（什麼、如何、可以…）
var cjkFunctional = map[rune]struct{}{
	'什': {}, '麼': {}, '怎': {}, '如': {}, '何': {}, '為': {}, '哪': {},
	'是': {}, '否': {}, '嗎': {}, '呢': {}, '的': {}, '了': {}, '在': {},
	'與': {}, '和': {}, '或': {}, '請': {}, '問': {}, '個': {}, '些': {},
	'裡': {}, '樣': {}, '可': {}, '以': {}, '需': {}, '要': {}, '應': {},
	'該': {}, '就': {}, '都': {}, '也': {}, '還': {}, '有': {}, '沒': {},
}

// keywords 抽取關鍵詞集合：ASCII 連續字元組成長度 >= 3 的小寫單詞（排除停用詞）；
// CJK 字元以相鄰雙字（bigram）為單位，兼顧中文無空格分詞（排除純功能字組合）。
func keywords(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var ascii []rune
	var prevCJK rune

	flushASCII := func() {
		if len(ascii) >= 3 {
			w := string(ascii)
			if _, stop := asciiStopwords[w]; !stop {
				out[w] = struct{}{}
			}
		}
		ascii = ascii[:0]
	}

	for _, r := range strings.ToLower(s) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			ascii = append(ascii, r)
			prevCJK = 0
		case unicode.Is(unicode.Han, r):
			flushASCII()
			if prevCJK != 0 {
				_, f1 := cjkFunctional[prevCJK]
				_, f2 := cjkFunctional[r]
				if !f1 || !f2 {
					out[string([]rune{prevCJK, r})] = struct{}{}
				}
			}
			prevCJK = r
		default:
			flushASCII()
			prevCJK = 0
		}
	}
	flushASCII()
	return out
}

// coverage 回傳 need 之中出現在 have 的比例，以及缺少的項目。
// need 為空時視為 1.0（沒有需要覆蓋的內容）。
func coverage(need, have map[string]struct{}) (float64, []string) {
	if len(need) == 0 {
		return 1.0, nil
	}
	hit := 0
	var missing []string
	for k := range need {
		if _, ok := have[k]; ok {
			hit++
		} else {
			missing = append(missing, k)
		}
	}
	return float64(hit) / float64(len(need)), missing
}

// splitSentences 以中英文句號、問號、驚嘆號、換行切句，去除空白句
func splitSentences(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '。', '！', '？', '.', '!', '?', '\n', '；', ';':
			return true
		}
		return false
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}
