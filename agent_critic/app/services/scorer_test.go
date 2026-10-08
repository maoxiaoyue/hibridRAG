package services

import "testing"

func TestScorePassesGroundedAnswer(t *testing.T) {
	s := NewLexicalScorer(0, 0, 0, 0) // 全預設：threshold 0.7, 權重 0.4/0.3/0.3

	question := "什麼是知識圖譜"
	answer := "知識圖譜是以實體與關係組成的結構化知識表示"
	sources := []string{"知識圖譜（knowledge graph）是以實體與關係組成的結構化知識表示，常用於檢索與推理"}

	res := s.Score(question, answer, sources)
	if !res.Passed {
		t.Fatalf("expected pass, got overall=%.2f (c=%.2f g=%.2f f=%.2f) feedback=%v",
			res.Overall, res.Completeness, res.Grounding, res.Faithfulness, res.Feedback)
	}
	if res.Completeness < 0.7 {
		t.Errorf("completeness = %.2f, want >= 0.7", res.Completeness)
	}
}

func TestScoreFailsWithoutSources(t *testing.T) {
	s := NewLexicalScorer(0, 0, 0, 0)

	res := s.Score("什麼是知識圖譜", "知識圖譜是一種資料結構", nil)
	if res.Passed {
		t.Fatalf("expected fail without sources, got overall=%.2f", res.Overall)
	}
	if res.Grounding != 0 || res.Faithfulness != 0 {
		t.Errorf("grounding/faithfulness = %.2f/%.2f, want 0/0", res.Grounding, res.Faithfulness)
	}
	if len(res.Feedback) == 0 {
		t.Error("expected feedback about missing sources")
	}
}

func TestScoreFlagsIncompleteAnswer(t *testing.T) {
	s := NewLexicalScorer(0, 0, 0, 0)

	// 答案完全沒有回應問題關鍵詞（離題）
	res := s.Score("如何設定 Cassandra 的 SAI 向量索引", "今天天氣很好", nil)
	if res.Completeness > 0.3 {
		t.Errorf("completeness = %.2f, want <= 0.3 for off-topic answer", res.Completeness)
	}
	if res.Passed {
		t.Error("expected off-topic answer to fail")
	}
	if len(res.Feedback) == 0 {
		t.Error("expected feedback about missing keywords")
	}
}

func TestKeywordsMixedCJKAndASCII(t *testing.T) {
	keys := keywords("使用 Apache AGE 建立圖譜")
	if _, ok := keys["apache"]; !ok {
		t.Error("expected ascii keyword 'apache'")
	}
	if _, ok := keys["age"]; !ok {
		t.Error("expected ascii keyword 'age'")
	}
	if _, ok := keys["圖譜"]; !ok {
		t.Error("expected CJK bigram '圖譜'")
	}
	// ASCII 詞與 CJK 之間不應該產生跨界 bigram
	if _, ok := keys["用a"]; ok {
		t.Error("unexpected cross-boundary bigram")
	}
}
