// tokens.go — Token budget estimation for ContextBuilder.
//
// 提供 TokenCounter 介面與一個 CJK-aware 啟發式預設實作（HeuristicCounter）。
// 之後若要接 tiktoken-go / claude tokenizer，只要實作同介面即可替換，
// builder 不需更動。
//
// 啟發式規則（足夠用於預算控制；非精確）：
//   - CJK 統一表意文字、日文假名、韓文音節：1 字 ≈ 1 token
//   - ASCII / Latin：~4 字 ≈ 1 token（OpenAI 經驗值）
//   - 其他 Unicode：~3 字 ≈ 1 token
//
// 截斷策略：以 rune 為單位逐字累計，超過 budget 即停（不會破 UTF-8）。
package context

import (
	"fmt"
	"unicode"
)

// TokenCounter 估算字串的 token 數，並可依預算截斷。
type TokenCounter interface {
	// Count 回傳 s 的估算 token 數。
	Count(s string) int
	// Truncate 將 s 截到不超過 maxTokens 的最長前綴，
	// 若有截斷則附加一行提示。maxTokens<=0 回原字串。
	Truncate(s string, maxTokens int) string
}

// HeuristicCounter 預設 CJK-aware 估算器；無外部依賴。
type HeuristicCounter struct{}

// NewHeuristicCounter 建立預設 TokenCounter。
func NewHeuristicCounter() TokenCounter { return HeuristicCounter{} }

// runeWeight 單一 rune 估算貢獻的 token 數（以「百分」為單位避免浮點累加誤差）。
// 回傳值單位：1/100 token。
func runeWeight(r rune) int {
	switch {
	case unicode.Is(unicode.Han, r),
		unicode.Is(unicode.Hiragana, r),
		unicode.Is(unicode.Katakana, r),
		unicode.Is(unicode.Hangul, r):
		return 100 // 1 token
	case r < 0x80:
		return 25 // ASCII：4 字元 ≈ 1 token
	default:
		return 33 // 其他 Unicode：~3 字元 ≈ 1 token
	}
}

// Count 估算 token 數（向上取整）。
func (HeuristicCounter) Count(s string) int {
	if s == "" {
		return 0
	}
	acc := 0
	for _, r := range s {
		acc += runeWeight(r)
	}
	// 向上取整：acc 單位是 1/100 token
	return (acc + 99) / 100
}

// Truncate 以 rune 走訪累計權重，超過 maxTokens*100 即停。
func (HeuristicCounter) Truncate(s string, maxTokens int) string {
	if maxTokens <= 0 || s == "" {
		return s
	}
	budget := maxTokens * 100
	acc := 0
	cut := -1
	runes := []rune(s)
	for i, r := range runes {
		w := runeWeight(r)
		if acc+w > budget {
			cut = i
			break
		}
		acc += w
	}
	if cut < 0 {
		return s
	}
	return string(runes[:cut]) +
		fmt.Sprintf("\n[... context truncated to ~%d tokens ...]\n", maxTokens)
}
