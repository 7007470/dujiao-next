package application

import (
	"encoding/json"
	"fmt"

	boostcontract "github.com/dujiao-next/internal/modules/boost/contract"
	boostdomain "github.com/dujiao-next/internal/modules/boost/domain"
	"github.com/dujiao-next/internal/modules/boost/domain/featuredef"
)

// 按步骤记录和结算完成量（用户 2026-10-29 选定方案 B）：执行端按步骤分别上报完成数，
// 每一步按自己的完成数 × 单价计价，不假设执行顺序。

// StepDone 执行端上报的某一步完成数。Seq 是 plan.steps 里的 seq（从 1 开始）。
type StepDone struct {
	Seq  int   `json:"seq"`
	Done int64 `json:"done"`
}

// stepsDone 读出任务当前每一步的完成数（与 plan.Steps 一一对应）。
// 旧任务（steps_done_json 为空）按步骤顺序从 units_done 推导，和 S5 初版的结算结果一致。
func stepsDone(task *boostdomain.Task, plan PlanSnapshot) []int64 {
	out := make([]int64, len(plan.Steps))
	if task.StepsDoneJSON != "" {
		var saved []int64
		if json.Unmarshal([]byte(task.StepsDoneJSON), &saved) == nil {
			for i := range out {
				if i < len(saved) {
					out[i] = saved[i]
				}
			}
			return out
		}
	}
	left := int64(task.UnitsDone)
	for i, st := range plan.Steps {
		d := st.Count
		if left < d {
			d = left
		}
		if d < 0 {
			d = 0
		}
		out[i] = d
		left -= d
	}
	return out
}

// applyStepReport 合并一次上报：每一步只增不减、不超过计划数；未上报的步骤保持不变。
// 写回 steps_done_json 和 units_done（= 各步之和），返回是否有超出计划被截断的部分。
func applyStepReport(task *boostdomain.Task, plan PlanSnapshot, report []StepDone) (clamped bool, err error) {
	done := stepsDone(task, plan)
	seen := map[int]bool{}
	for _, r := range report {
		i := r.Seq - 1
		if i < 0 || i >= len(plan.Steps) {
			return false, invalid(boostcontract.ErrInvalid, "步骤 seq=%d 不存在（共 %d 步）", r.Seq, len(plan.Steps))
		}
		if seen[r.Seq] {
			return false, invalid(boostcontract.ErrInvalid, "步骤 seq=%d 重复上报", r.Seq)
		}
		seen[r.Seq] = true
		v := r.Done
		if v > plan.Steps[i].Count {
			v = plan.Steps[i].Count
			clamped = true
		}
		if v > done[i] { // 只增不减
			done[i] = v
		}
	}
	setStepsDone(task, done)
	return clamped, nil
}

// completeAllSteps 全部完成（执行端完成时没有上报步骤）。
func completeAllSteps(task *boostdomain.Task, plan PlanSnapshot) {
	done := make([]int64, len(plan.Steps))
	for i, st := range plan.Steps {
		done[i] = st.Count
	}
	setStepsDone(task, done)
}

func setStepsDone(task *boostdomain.Task, done []int64) {
	var sum int64
	for _, d := range done {
		sum += d
	}
	raw, _ := json.Marshal(done)
	task.StepsDoneJSON = string(raw)
	task.UnitsDone = int(sum)
}

func stepsPlan(plan PlanSnapshot) featuredef.Plan {
	return plan.compilePlan()
}

// SettlementLine 结算明细的一行（展示给用户和后台）。
type SettlementLine struct {
	Seq        int    `json:"seq"`
	Label      string `json:"label"`
	Count      int64  `json:"count"`
	Done       int64  `json:"done"`
	UnitPoints int64  `json:"unit_points"`
	Points     int64  `json:"points"` // 完成数 × 单价（原价）
}

// Settlement 结算明细：每一步完成数 × 单价、固定部分、折扣、最终按完成量应扣；任务结束后附实际扣除 / 退回和原因。
type Settlement struct {
	Lines        []SettlementLine `json:"lines"`
	FixedPoints  int64            `json:"fixed_points"` // 基础价 + 附加项（开始执行后才收）
	FixedCharged bool             `json:"fixed_charged"`
	RawPoints    int64            `json:"raw_points"` // 原价小计
	Rate         int              `json:"rate"`       // 折扣率（100 = 不打折）
	ByUnits      int64            `json:"by_units"`   // 按完成量应扣（折后、不超过冻结）
	Held         int64            `json:"held"`
	Consumed     int64            `json:"consumed"`
	Released     int64            `json:"released"`
	Final        bool             `json:"final"` // 已结算
	Rule         string           `json:"rule"`  // 本次结算用的规则说明
}

// buildSettlement 计算结算明细。任务未结束时 Consumed / Released 是"如果现在结束"的预估（按完成量）。
func buildSettlement(task *boostdomain.Task, plan PlanSnapshot, snap TaskSnapshot) Settlement {
	done := stepsDone(task, plan)
	out := Settlement{FixedPoints: plan.FixedPoints, Rate: plan.Rate, Held: task.PointsHeld}
	var any bool
	raw := int64(0)
	for i, st := range plan.Steps {
		d := done[i]
		if d > 0 {
			any = true
		}
		line := SettlementLine{Seq: st.Seq, Label: st.Label, Count: st.Count, Done: d, UnitPoints: st.UnitPoints, Points: d * st.UnitPoints}
		raw += line.Points
		out.Lines = append(out.Lines, line)
	}
	if any {
		raw += plan.FixedPoints
		out.FixedCharged = plan.FixedPoints > 0
	}
	out.RawPoints = raw
	out.ByUnits = featuredef.PointsForSteps(stepsPlan(plan), done)
	if boostdomain.IsTerminalTask(task.Status) {
		out.Final = true
		out.Consumed, out.Released = task.PointsConsumed, task.PointsReleased
	} else {
		out.Consumed, out.Released = out.ByUnits, task.PointsHeld-out.ByUnits
	}
	out.Rule = settlementRule(task, snap, out)
	return out
}

func settlementRule(task *boostdomain.Task, snap TaskSnapshot, st Settlement) string {
	if !st.Final {
		return "进行中：按每一步已完成次数 × 单价预估，结束时以执行端最终上报为准"
	}
	if task.Status == boostdomain.TaskStatusFail {
		info, known := boostdomain.LookupErrorCode(task.ErrorCode)
		switch snap.RefundPolicy {
		case boostdomain.RefundNone:
			return "失败：该功能的退款策略为不退款，扣除全部冻结积分"
		case boostdomain.RefundFullOnSystemFail:
			if known && info.System {
				return fmt.Sprintf("失败（%s，系统原因）：该功能系统原因失败全额退回", info.Label)
			}
		}
		label := task.ErrorCode
		if known {
			label = info.Label
		}
		return fmt.Sprintf("失败（%s）：按每一步已完成次数 × 单价扣除，其余退回", label)
	}
	if task.Status == boostdomain.TaskStatusSuccess && st.Consumed == st.Held {
		return "全部完成：扣除冻结的全部积分"
	}
	return "按每一步已完成次数 × 单价扣除，其余退回"
}

func stepDoneList(task *boostdomain.Task, plan PlanSnapshot) []StepDone {
	done := stepsDone(task, plan)
	out := make([]StepDone, len(plan.Steps))
	for i, st := range plan.Steps {
		out[i] = StepDone{Seq: st.Seq, Done: done[i]}
	}
	return out
}
