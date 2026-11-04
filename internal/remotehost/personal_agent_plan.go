package remotehost

import (
	"net/http"
	"strconv"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: 依赖 personal_agent.go 的 paSeam/paID/paRefuse 与 internal/personalagent 的 GoalPlan/GoalFacts/Facts 客户端方法
 * [OUTPUT]: 对外提供 GET /api/v3/personal-agent/goals/{id}/plan（Goal 的计划：成功条件、里程碑及其由任务事实推导的状态、修订轨迹，外加该 Goal 的事实账本）
 *           与 GET …/assistants/{id}/facts?limit=（Assistant 的事实账本，新到旧），bootstrap 的 capabilities.personalAgentPlans
 * [POS]: Assistants 3.1 U3 的 Host 面（只读）：计划由 Coordinator/桌面写入 Go 核心，Supervisor 推进并只凭已提交的任务状态判定“已验证”；
 *        手机只看，不写计划。事实是核心自己写的句子（id/标题/计数），不是 Agent 的话，也不含任务结果或错误文本；日志只记设备、操作名与结果码
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const maxPAFacts = 100

func (s *Server) handlePAGoalPlan(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	plan, err := c.GoalPlan(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "goal.plan", err)
		return
	}
	facts, err := c.GoalFacts(r.Context(), id)
	if err != nil {
		s.paRefuse(w, dev, "goal.facts", err)
		return
	}
	if facts == nil {
		facts = []personalagent.Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "plan": plan, "facts": facts})
}

func (s *Server) handlePAFacts(w http.ResponseWriter, r *http.Request, dev Device) {
	c := s.paSeam(w, r)
	if c == nil {
		return
	}
	id, ok := paID(w, r.PathValue("id"))
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPAFacts {
			writeError(w, http.StatusBadRequest, "bad_request", "")
			return
		}
		limit = n
	}
	facts, err := c.Facts(r.Context(), id, limit)
	if err != nil {
		s.paRefuse(w, dev, "assistant.facts", err)
		return
	}
	if facts == nil {
		facts = []personalagent.Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema": 1, "facts": facts})
}
