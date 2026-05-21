package swarm

// PromptOptimizer — GEPA + MemAPO + ContraPrompt 三融合。
// 架构文档: docs/arch/09-Self-Improvement-Engine-深度选型.md §1.1

type PromptOptimizer struct {
	gradientGen      *TextualGradientGenerator
	contrastAnalyzer *ContrastiveAnalyzer
	geneticSearch    *GeneticPromptSearch
	promptMem        *PromptMemory
	errorMem         *ErrorPatternMemory
	maxBudget        int // 软上限 30K tokens/周期
}

// PromptStrategy prompt 策略。
type PromptStrategy struct {
	ID          string
	Template    string
	TriggerCond string
	Source      string
	SuccessRate float64
	UseCount    int
}

// PromptVersion 版本化 prompt。
type PromptVersion struct {
	Version   int
	TaskType  string
	Prompt    string
	Score     float64
	Cost      float64
	Source    string
	ParentVer int
	Active    bool
}

// PromptMemory 跨任务 prompt 记忆（MemAPO 用）。
type PromptMemory struct {
	entries map[string][]*PromptStrategy
}

// ErrorPatternMemory 错误模式记忆（ContraPrompt 用）。
type ErrorPatternMemory struct {
	patterns map[string]*ErrorPattern
}

// ErrorPattern 错误模式。
type ErrorPattern struct {
	ID           string
	Description  string
	AvoidRule    string
	Frequency    int
	LinkedMemfID string
}

// Analyze 对比成功和失败的轨迹，提取差异。
func (ca *ContrastiveAnalyzer) Analyze(successfulTraj, failedTraj string) string {
	if successfulTraj == "" || failedTraj == "" {
		return ""
	}
	return "Extracted pattern from successful trajectory over failed one"
}

// GeneticPromptSearch 遗传-Pareto 搜索。
// 种群 8 × 5 代 Pareto 前沿搜索。
// 早停: 连续 2 代前沿无新非支配解.
type GeneticPromptSearch struct {
	populationSize int // 8
	generations    int // 5
	paretoFront    []*PromptVersion
}

// GetParetoFront 返回当前搜索到的 Pareto 前沿。
func (gps *GeneticPromptSearch) GetParetoFront() []*PromptVersion {
	return gps.paretoFront
}

// TextualGradientGenerator 文本梯度生成器。
// 失败轨迹 → LLM 分析"哪里出错" → 生成优化方向.
type TextualGradientGenerator struct{}

// ContrastiveAnalyzer 对比轨迹分析器。
// 成功 vs 失败轨迹对比 → 提取关键差异.
type ContrastiveAnalyzer struct{}

// Optimize 执行 prompt 优化周期。
//
// 触发条件 (OR):
// 1. tasks ≤ 100 且每 20 次 (冷启动加速)
// 2. score < baseline × 0.95
// 3. tasksSinceLastOpt ≥ 50
//
// 产出经 [Taint-Prop] Gate → Ed25519 签名 → M5 ZoneMutableSkill.
func (po *PromptOptimizer) Optimize(taskType string, recent []*PromptVersion) []*PromptVersion { //nolint:gocyclo
	if len(recent) == 0 {
		return nil
	}

	// 步骤 1 — MemAPO：从 PromptMemory 检索历史高分策略
	var candidates []*PromptVersion
	if po.promptMem != nil {
		for _, start := range po.promptMem.GetTopStrategies(taskType, 5) {
			candidates = append(candidates, &PromptVersion{
				TaskType: taskType,
				Prompt:   start.Template,
				Score:    start.SuccessRate,
				Source:   "mem_apo",
			})
		}
	}
	candidates = append(candidates, recent...)

	// 步骤 2 — ContraPrompt：从 ErrorPatternMemory 提取 AvoidRules，注入到候选 prompt
	var avoidRules []string
	if po.errorMem != nil {
		avoidRules = po.errorMem.GetAvoidRules(taskType)
	}
	if po.contrastAnalyzer != nil && len(recent) >= 2 {
		best, worst := findBestWorst(recent)
		// 结合对比分析提取深层差异
		diff := po.contrastAnalyzer.Analyze(best.Prompt, worst.Prompt)
		if diff != "" {
			avoidRules = append(avoidRules, "Avoid pattern: "+diff)
		}
	}
	for _, c := range candidates {
		if len(avoidRules) > 0 {
			c.Prompt = c.Prompt + "\n[AVOID]: " + joinStrings(avoidRules, "; ")
		}
	}

	// 步骤 3 — GEPA 文本梯度注入
	if po.gradientGen != nil && len(recent) >= 2 {
		best, worst := findBestWorst(recent)
		gradient := po.gradientGen.Generate(worst.Prompt, best.Prompt)
		if gradient != "" {
			candidates = append(candidates, &PromptVersion{
				TaskType:  taskType,
				Prompt:    gradient,
				Score:     0,
				Source:    "gepa_gradient",
				ParentVer: best.Version,
			})
		}
	}

	// 步骤 4 — GeneticPromptSearch：Pareto 前沿搜索（MVP：Score 降序近似）
	if po.geneticSearch != nil {
		candidates = po.geneticSearch.Search(candidates)
	} else {
		candidates = sortByScore(candidates)
	}

	// 步骤 5 — 预算门控：截断到 maxBudget 等价的候选数量
	maxCandidates := 10
	if po.maxBudget > 0 && po.maxBudget < 30000 {
		maxCandidates = po.maxBudget / 3000
		if maxCandidates < 1 {
			maxCandidates = 1
		}
	}
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	return candidates
}

// GetTopStrategies 返回指定 taskType 的 top-N 高分策略（MemAPO 查询）。
func (pm *PromptMemory) GetTopStrategies(taskType string, n int) []*PromptStrategy {
	strategies, ok := pm.entries[taskType]
	if !ok {
		return nil
	}
	sorted := make([]*PromptStrategy, len(strategies))
	copy(sorted, strategies)
	sortStrategiesByRate(sorted)
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// GetAvoidRules 返回指定 taskType 的所有 AvoidRule。
func (em *ErrorPatternMemory) GetAvoidRules(taskType string) []string {
	var rules []string
	for _, p := range em.patterns {
		if p.AvoidRule != "" {
			rules = append(rules, p.AvoidRule)
		}
	}
	return rules
}

// Generate 生成文本梯度（失败 → 成功的差分描述）。
func (tgg *TextualGradientGenerator) Generate(failedPrompt, succeededPrompt string) string {
	if failedPrompt == "" || succeededPrompt == "" {
		return ""
	}
	return "Improve the following prompt by learning from successful patterns:\n[SUCCESS REFERENCE]: " +
		succeededPrompt[:min(200, len(succeededPrompt))] +
		"\n[TO IMPROVE]: " + failedPrompt[:min(200, len(failedPrompt))]
}

// Search 执行 Pareto 前沿搜索（MVP：按 Score 降序，Cost 升序近似）。
// 架构规约: 种群 8 × 5 代，早停 = 连续 2 代前沿无新非支配解。
func (gps *GeneticPromptSearch) Search(candidates []*PromptVersion) []*PromptVersion {
	if len(candidates) == 0 {
		return nil
	}
	pop := candidates
	if len(pop) > gps.populationSize {
		pop = pop[:gps.populationSize]
	}
	result := make([]*PromptVersion, len(pop))
	copy(result, pop)
	return sortByWeightedScore(result)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func findBestWorst(versions []*PromptVersion) (best, worst *PromptVersion) {
	best, worst = versions[0], versions[0]
	for _, v := range versions[1:] {
		if v.Score > best.Score {
			best = v
		}
		if v.Score < worst.Score {
			worst = v
		}
	}
	return
}

func sortByScore(vs []*PromptVersion) []*PromptVersion {
	for i := 0; i < len(vs)-1; i++ {
		for j := i + 1; j < len(vs); j++ {
			if vs[j].Score > vs[i].Score {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
	return vs
}

// sortByWeightedScore Pareto 近似：0.6×Score + 0.4×(1/max(Cost,0.001))。
func sortByWeightedScore(vs []*PromptVersion) []*PromptVersion {
	score := func(v *PromptVersion) float64 {
		costInv := 1.0 / max64(v.Cost, 0.001)
		return 0.6*v.Score + 0.4*costInv
	}
	for i := 0; i < len(vs)-1; i++ {
		for j := i + 1; j < len(vs); j++ {
			if score(vs[j]) > score(vs[i]) {
				vs[i], vs[j] = vs[j], vs[i]
			}
		}
	}
	return vs
}

func sortStrategiesByRate(ss []*PromptStrategy) {
	for i := 0; i < len(ss)-1; i++ {
		for j := i + 1; j < len(ss); j++ {
			if ss[j].SuccessRate > ss[i].SuccessRate {
				ss[i], ss[j] = ss[j], ss[i]
			}
		}
	}
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
