package models

// 對應 agent_gateway ClientGateway 的 Graph* RPC（原始事實來源：agent_graph）。

// GraphEntity 一個知識圖譜實體。
type GraphEntity struct {
	Name        string  `json:"name"`
	Category    string  `json:"category,omitempty"`
	Description string  `json:"description,omitempty"`
	SourceFile  string  `json:"source_file,omitempty"`
	Score       float64 `json:"score,omitempty"`  // 混合搜尋時填入向量相似度
	Degree      int     `json:"degree,omitempty"` // 全圖關係數（後端回填；節點大小/關鍵節點判斷用）
}

// GraphRelation 兩個實體之間的一條關係。
type GraphRelation struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

// GraphContext 一個實體的區域圖遍歷結果。
type GraphContext struct {
	Center    GraphEntity     `json:"center"`
	Neighbors []GraphEntity   `json:"neighbors"`
	Relations []GraphRelation `json:"relations"`
}

// GraphHybridSearchResult 向量種子 + 各自的圖遍歷結果。
type GraphHybridSearchResult struct {
	Seeds   []GraphEntity  `json:"seeds"`
	Context []GraphContext `json:"context"`
}

// GraphProposedRelation 一筆待人工審核的低信心關係候選。
type GraphProposedRelation struct {
	ID           string  `json:"id"`
	SourceEntity string  `json:"source_entity"`
	TargetEntity string  `json:"target_entity"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	SourceFile   string  `json:"source_file,omitempty"`
	ExtractedBy  string  `json:"extracted_by,omitempty"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
}
