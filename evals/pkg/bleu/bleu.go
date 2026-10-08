// Package bleu 計算簡化版 BLEU-4（單一參考句、加性平滑），作為 eval dataset
// 中「有明確標準答案」案例的其中一項訊號。
//
// 定位：BLEU 對開放式生成很不友善（換句話說會被扣分），只適合事實型 QA
// 這類有標準答案的案例，不該是唯一或主要指標——LLM-as-judge（見 evals 計畫
// Phase 3）評的是語意，才是主指標。這裡先做免 LLM、免服務依賴的 baseline，
// 讓 runner 在 agent_critic 的 judge 接上之前就有東西可看。
package bleu

import (
	"math"
	"strings"
	"unicode"
)

// smoothing 避免 log(0)：candidate 短於某個 n-gram 階數、或該階完全不重疊時，
// 用極小值代替 0，讓幾何平均不會直接崩成 0（仍會被強烈拉低分數，但保留可比較性）。
const smoothing = 1e-9

const maxN = 4

// tokenize 依 ASCII 單詞（小寫）與 CJK 逐字切 token。
//
// CJK 逐字（而非 agent_critic/scorer.go 那種 bigram）是 BLEU 的標準中文處理
// 方式（sacrebleu 的 zh tokenizer 同樣逐字切）——n-gram 本身就會在相鄰字之間
// 形成語意關聯，不需要先手動 bigram 再對 bigram 取 n-gram。
func tokenize(s string) []string {
	var out []string
	var ascii []rune
	flush := func() {
		if len(ascii) > 0 {
			out = append(out, string(ascii))
			ascii = ascii[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			ascii = append(ascii, r)
		case unicode.Is(unicode.Han, r):
			flush()
			out = append(out, string(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

// ngramCounts 回傳 tokens 中每個長度 n 的連續子序列的出現次數。
// tokens 短於 n 時回傳空 map（呼叫端據此把該階精確度視為 0，走 smoothing）。
func ngramCounts(tokens []string, n int) map[string]int {
	counts := map[string]int{}
	if len(tokens) < n {
		return counts
	}
	for i := 0; i+n <= len(tokens); i++ {
		key := strings.Join(tokens[i:i+n], "\x1f")
		counts[key]++
	}
	return counts
}

// Score 計算 candidate 對 reference 的 BLEU-4：1-gram 到 4-gram 的 clipped
// precision 幾何平均，乘上 brevity penalty（candidate 比 reference 短才懲罰）。
// 回傳 0-1，越高越接近參考答案。candidate 或 reference 為空白時回傳 0。
func Score(candidate, reference string) float64 {
	candTokens := tokenize(candidate)
	refTokens := tokenize(reference)
	if len(candTokens) == 0 || len(refTokens) == 0 {
		return 0
	}

	logSum := 0.0
	for n := 1; n <= maxN; n++ {
		candCounts := ngramCounts(candTokens, n)
		refCounts := ngramCounts(refTokens, n)

		var clipped, total int
		for gram, c := range candCounts {
			total += c
			if rc := refCounts[gram]; rc < c {
				clipped += rc
			} else {
				clipped += c
			}
		}

		precision := smoothing
		if total > 0 {
			p := float64(clipped) / float64(total)
			if p > 0 {
				precision = p
			}
		}
		logSum += math.Log(precision)
	}
	geoMean := math.Exp(logSum / maxN)

	bp := 1.0
	if len(candTokens) < len(refTokens) {
		bp = math.Exp(1 - float64(len(refTokens))/float64(len(candTokens)))
	}
	return geoMean * bp
}
