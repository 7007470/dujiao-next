package featuredef

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Normalize 返回补全默认值后的定义，对应 catalog.js normalize。只应对已通过 Validate 的定义调用。
func Normalize(def Value) Value {
	or := func(v Value, fallback Value) Value {
		if v.Truthy() {
			return v
		}
		return fallback
	}
	maxUnits := NumberValue(500)
	if limits := def.Get("limits"); limits.Truthy() {
		maxUnits = or(limits.Get("max_units"), maxUnits)
	}
	keys := []string{"schema_version", "code", "name", "description", "capability", "timeout_minutes",
		"max_active_per_user", "refund_policy", "member_discount", "base_points", "catalogs", "fields", "limits"}
	return objectValue(keys, map[string]Value{
		"schema_version":      NumberValue(SchemaVersion),
		"code":                def.Get("code"),
		"name":                def.Get("name"),
		"description":         or(def.Get("description"), StringValue("")),
		"capability":          or(def.Get("capability"), def.Get("code")),
		"timeout_minutes":     or(def.Get("timeout_minutes"), NumberValue(120)),
		"max_active_per_user": or(def.Get("max_active_per_user"), NumberValue(3)),
		"refund_policy":       or(def.Get("refund_policy"), StringValue("prorate")),
		"member_discount":     BoolValue(!StrictEqual(def.Get("member_discount"), BoolValue(false))),
		"base_points":         or(def.Get("base_points"), NumberValue(0)),
		"catalogs":            or(def.Get("catalogs"), objectValue(nil, map[string]Value{})),
		"fields":              def.Get("fields"),
		"limits":              objectValue([]string{"max_units"}, map[string]Value{"max_units": maxUnits}),
	})
}

// StepRef 指明执行步骤对应的字段和条目，执行端据此定位关卡。
type StepRef struct {
	Implicit bool // 没有按量计费字段时的隐含步骤
	Field    string
	Item     string // stage_picker 条目 code
	Group    string // stage_picker 条目所在分组 code
	Mode     Value  // stage_picker 的计价模式（cost_by 字段的值）；没有 cost_by 时为 null
	isStage  bool
}

// MarshalJSON 输出与原型一致的键：{implicit} / {field} / {field, item, group, mode}。
func (r StepRef) MarshalJSON() ([]byte, error) {
	switch {
	case r.Implicit:
		return []byte(`{"implicit":true}`), nil
	case !r.isStage:
		return json.Marshal(map[string]string{"field": r.Field})
	}
	keys := []string{"field", "item", "group", "mode"}
	return objectValue(keys, map[string]Value{
		"field": StringValue(r.Field), "item": StringValue(r.Item), "group": StringValue(r.Group), "mode": r.Mode,
	}).MarshalJSON()
}

// Step 是一个执行步骤：Count 个执行单位，每个 UnitPoints 积分（原价，未打折）。
type Step struct {
	Label      string  `json:"label"`
	Count      int64   `json:"count"`
	UnitPoints int64   `json:"unitPoints"`
	UnitLabel  string  `json:"unitLabel"`
	Ref        StepRef `json:"ref"`
	Seq        int     `json:"seq"`            // 放在 ref 之后，与原型 JSON 键顺序一致
	Done       *int64  `json:"done,omitempty"` // 只在 StepProgress 的结果里有值
}

// FixedCharge 是不随执行量变化的积分（基础价、附加项）。
type FixedCharge struct {
	Label  string `json:"label"`
	Points int64  `json:"points"`
}

// BreakdownLine 是报价明细的一行。
type BreakdownLine struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
	Points int64  `json:"points"`
}

// Plan 是 Compile 的结果：规范化参数、执行步骤和积分。JSON 键名与原型一致，前端直接使用。
type Plan struct {
	OK          bool            `json:"ok"`
	Values      Value           `json:"values"`      // 规范化后的参数，隐藏字段不出现
	Visible     Value           `json:"visible"`     // 字段 key → 是否显示
	FieldErrors Value           `json:"fieldErrors"` // 字段 key → 第一条错误
	Errors      []string        `json:"errors"`
	Steps       []Step          `json:"steps"`
	Fixed       []FixedCharge   `json:"fixed"`
	Units       int64           `json:"units"`
	FixedPoints int64           `json:"fixedPoints"`
	RawTotal    int64           `json:"rawTotal"`
	Total       int64           `json:"total"`
	Breakdown   []BreakdownLine `json:"breakdown"`
	Rate        int             `json:"rate"`
}

// orderedObject 按插入顺序收集对象属性。
type orderedObject struct {
	keys  []string
	props map[string]Value
}

func newOrderedObject() *orderedObject { return &orderedObject{props: map[string]Value{}} }

func (o *orderedObject) set(key string, v Value) {
	if _, exists := o.props[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.props[key] = v
}

func (o *orderedObject) has(key string) bool  { _, ok := o.props[key]; return ok }
func (o *orderedObject) get(key string) Value { return o.props[key] }
func (o *orderedObject) value() Value         { return objectValue(o.keys, o.props) }

// ApplyRate 按会员折扣率（百分比）计算合计，向下取整，至少 1 积分。折扣只在合计上算一次。
func ApplyRate(raw int64, rate int) int64 {
	if raw == 0 {
		return 0
	}
	if rate == 0 || rate >= 100 {
		return raw
	}
	discounted := int64(math.Floor(float64(raw) * float64(rate) / 100))
	if discounted < 1 {
		return 1
	}
	return discounted
}

// matchWhen 对应 catalog.js matchWhen：visible_when 的每个条件都满足才显示。
func matchWhen(vw Value, values *orderedObject) bool {
	if !vw.Truthy() {
		return true
	}
	for _, k := range vw.Keys() {
		want := vw.Get(k)
		candidates := []Value{want}
		if want.IsArray() {
			candidates = want.Items()
		}
		actual := values.get(k)
		matched := false
		for _, c := range candidates {
			if StrictEqual(c, actual) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

type catalogItem struct {
	item      Value
	group     string
	groupName string
}

func itemIndex(cat Value) map[string]catalogItem {
	index := map[string]catalogItem{}
	for _, g := range cat.Get("groups").Items() {
		group, _ := g.Get("code").Str()
		groupName, _ := g.Get("name").Str()
		for _, it := range g.Get("items").Items() {
			if code, ok := it.Get("code").Str(); ok {
				index[code] = catalogItem{item: it, group: group, groupName: groupName}
			}
		}
	}
	return index
}

// itemCost 返回条目在某模式下的单价；ok=false 表示该模式不可用。
func itemCost(it Value, mode Value) (int64, bool) {
	cost := it.Get("cost")
	if cost.IsInt() {
		return int64(cost.n), true
	}
	if mode.Kind() == Undefined || mode.Kind() == Null {
		return 0, false
	}
	if !cost.IsObject() {
		return 0, false
	}
	if _, own := cost.props[jsString(mode)]; !own {
		return 0, false
	}
	return int64(cost.Get(jsString(mode)).n), true
}

func findOption(f Value, v Value) (Value, bool) {
	for _, o := range f.Get("options").Items() {
		if StrictEqual(o.Get("value"), v) {
			return o, true
		}
	}
	return Value{}, false
}

func isEmptyInput(v Value) bool {
	return v.Kind() == Undefined || v.Kind() == Null || StrictEqual(v, StringValue(""))
}

// Compile 把用户填写的参数编译成规范化参数、执行步骤和积分，对应 catalog.js compile。
// def 必须已通过 Validate；discountRate 是会员折扣百分比（100 或 0 表示不打折）。
// 服务端下单时只信任这里的结果，前端传来的价格一律不用。
func Compile(def Value, raw Value, discountRate int) Plan {
	d := Normalize(def)
	rate := 100
	if d.Get("member_discount").b && discountRate != 0 {
		rate = discountRate
	}
	input := raw
	if !input.IsObject() {
		input = objectValue(nil, map[string]Value{})
	}
	values := newOrderedObject()
	visible := newOrderedObject()
	fieldErrors := newOrderedObject()
	var fixed []FixedCharge
	var steps []Step
	var breakdown []BreakdownLine
	fe := func(key, msg string) {
		if !fieldErrors.has(key) {
			fieldErrors.set(key, StringValue(msg))
		}
	}
	addFixed := func(label, detail string, points int64) {
		fixed = append(fixed, FixedCharge{Label: label, Points: points})
		breakdown = append(breakdown, BreakdownLine{Label: label, Detail: detail, Points: points})
	}

	if base := d.Get("base_points"); base.Truthy() {
		addFixed("基础服务", "每个任务一次", int64(base.n))
	}

	fields := d.Get("fields").Items()
	for _, f := range fields {
		key, _ := f.Get("key").Str()
		label := jsString(f.Get("label"))
		vis := matchWhen(f.Get("visible_when"), values)
		visible.set(key, BoolValue(vis))
		if !vis {
			continue // 隐藏字段不取值、不计价、不校验
		}
		v := input.Get(key)
		fieldType, _ := f.Get("type").Str()
		switch fieldType {
		case "radio", "select":
			if isEmptyInput(v) {
				v = f.Get("default")
			}
			if !v.IsDefined() {
				if f.Get("required").Truthy() {
					fe(key, "请选择"+label)
				}
				break
			}
			opt, ok := findOption(f, v)
			if !ok {
				fe(key, label+"选项不合法")
				break
			}
			values.set(key, v)
			if pts := opt.Get("points"); pts.Truthy() {
				addFixed(label+"："+jsString(opt.Get("label")), "附加", int64(pts.n))
			}

		case "checkbox":
			if !v.IsDefined() {
				v = f.Get("default")
				if !v.Truthy() {
					v = ArrayValue(nil)
				}
			}
			if !v.IsArray() {
				v = ArrayValue([]Value{v})
			}
			selected := valueSet{}
			for _, x := range v.Items() {
				selected.add(x)
			}
			okVals := valueSet{}
			var picked []Value // 去重并按选项顺序
			for _, o := range f.Get("options").Items() {
				okVals.add(o.Get("value"))
				if selected.has(o.Get("value")) {
					picked = append(picked, o.Get("value"))
				}
			}
			for _, x := range v.Items() {
				if !okVals.has(x) {
					fe(key, label+"含有不合法的选项")
					break
				}
			}
			if f.Get("required").Truthy() && len(picked) == 0 {
				fe(key, "请至少选择一项"+label)
			}
			if minSel := f.Get("min_select"); minSel.Truthy() && float64(len(picked)) < minSel.n {
				fe(key, fmt.Sprintf("%s至少选 %s 项", label, jsString(minSel)))
			}
			if maxSel := f.Get("max_select"); maxSel.Truthy() && float64(len(picked)) > maxSel.n {
				fe(key, fmt.Sprintf("%s最多选 %s 项", label, jsString(maxSel)))
			}
			values.set(key, ArrayValue(picked))
			for _, x := range picked {
				opt, _ := findOption(f, x)
				if pts := opt.Get("points"); pts.Truthy() {
					addFixed(label+"："+jsString(opt.Get("label")), "附加", int64(pts.n))
				}
			}

		case "switch":
			if !v.IsDefined() || StrictEqual(v, StringValue("")) {
				v = BoolValue(StrictEqual(f.Get("default"), BoolValue(true)))
			}
			on := StrictEqual(v, BoolValue(true)) || StrictEqual(v, StringValue("true")) || StrictEqual(v, NumberValue(1))
			values.set(key, BoolValue(on))
			if pts := f.Get("points"); on && pts.Truthy() {
				addFixed(label, "附加", int64(pts.n))
			}

		case "number":
			if isEmptyInput(v) {
				v = f.Get("default")
			}
			if !v.IsDefined() {
				if f.Get("required").Truthy() {
					fe(key, "请填写"+label)
				}
				break
			}
			n := NumberValue(jsToNumber(v))
			if !n.IsInt() {
				fe(key, label+"必须是整数")
				break
			}
			minV, maxV := f.Get("min"), f.Get("max")
			unit := ""
			if u := f.Get("unit"); u.Truthy() {
				unit = jsString(u)
			}
			if n.n < minV.n || n.n > maxV.n {
				fe(key, fmt.Sprintf("%s需在 %s–%s%s 之间", label, jsString(minV), jsString(maxV), unit))
				break
			}
			values.set(key, n)
			if pp := f.Get("points_per"); pp.IsDefined() && n.n > 0 {
				unitPts := int64(pp.n)
				count := int64(n.n)
				steps = append(steps, Step{Label: label, Count: count, UnitPoints: unitPts, UnitLabel: unit, Ref: StepRef{Field: key}})
				breakdown = append(breakdown, BreakdownLine{Label: label, Detail: fmt.Sprintf("%d%s × %d", count, unit, unitPts), Points: unitPts * count})
			}

		case "text":
			if !v.IsDefined() || v.Kind() == Null {
				v = StringValue("")
			}
			text := jsTrim(jsString(v))
			if f.Get("required").Truthy() && text == "" {
				fe(key, "请填写"+label)
			}
			maxLen := 100.0
			if ml := f.Get("maxlength"); ml.Truthy() {
				maxLen = ml.n
			}
			if float64(jsLen(text)) > maxLen {
				fe(key, fmt.Sprintf("%s最多 %s 个字", label, jsNumberString(maxLen)))
			}
			if pat := f.Get("pattern"); text != "" && pat.Truthy() {
				if re, err := compilePattern(jsString(pat)); err != nil || !re.MatchString(text) {
					fe(key, label+"格式不正确")
				}
			}
			if text != "" {
				values.set(key, StringValue(text))
			}

		case "stage_picker":
			steps = compileStagePicker(d, f, key, label, v, values, fe, steps, &breakdown)
		}
	}

	// 没有按量计费的字段时，生成一个隐含步骤，代表"执行一次"
	if len(steps) == 0 {
		steps = append(steps, Step{Label: jsString(d.Get("name")), Count: 1, UnitPoints: 0, UnitLabel: "次", Ref: StepRef{Implicit: true}})
	}
	var units, fixedPoints, unitTotal int64
	for i := range steps {
		steps[i].Seq = i + 1
		units += steps[i].Count
		unitTotal += steps[i].UnitPoints * steps[i].Count
	}
	for _, x := range fixed {
		fixedPoints += x.Points
	}
	rawTotal := fixedPoints + unitTotal
	total := ApplyRate(rawTotal, rate)
	if rawTotal > total {
		breakdown = append(breakdown, BreakdownLine{Label: "会员折扣", Detail: jsNumberString(float64(rate)/10) + " 折", Points: total - rawTotal})
	}
	errors := []string{}
	for _, k := range fieldErrors.keys {
		msg, _ := fieldErrors.get(k).Str()
		errors = append(errors, msg)
	}
	maxUnits := d.Get("limits").Get("max_units")
	if float64(units) > maxUnits.n {
		errors = append(errors, fmt.Sprintf("单个任务最多 %s 个执行单位，当前 %d", jsString(maxUnits), units))
	}
	if len(errors) == 0 && total <= 0 {
		errors = append(errors, "合计积分为 0，请检查功能定价")
	}
	if fixed == nil {
		fixed = []FixedCharge{}
	}
	if breakdown == nil {
		breakdown = []BreakdownLine{}
	}
	return Plan{
		OK: len(errors) == 0, Values: values.value(), Visible: visible.value(), FieldErrors: fieldErrors.value(),
		Errors: errors, Steps: steps, Fixed: fixed, Units: units, FixedPoints: fixedPoints,
		RawTotal: rawTotal, Total: total, Breakdown: breakdown, Rate: rate,
	}
}

func compileStagePicker(d, f Value, key, label string, v Value, values *orderedObject, fe func(string, string), steps []Step, breakdown *[]BreakdownLine) []Step {
	cat := d.Get("catalogs").Get(jsString(f.Get("catalog")))
	catLabel := jsString(cat.Get("label"))
	index := itemIndex(cat)
	costBy := f.Get("cost_by")
	mode := Value{kind: Null}
	if costBy.Truthy() {
		mode = values.get(jsString(costBy))
	}
	count := f.Get("count")
	countVisible := count.Truthy() && matchWhen(count.Get("visible_when"), values)
	if isEmptyInput(v) {
		v = ArrayValue(nil)
	}
	if !v.IsArray() {
		v = ArrayValue([]Value{v})
	}

	var costByField Value
	for _, x := range d.Get("fields").Items() {
		if StrictEqual(x.Get("key"), costBy) {
			costByField = x
			break
		}
	}

	var list []Value
	dup := map[string]bool{}
	for _, entry := range v.Items() {
		code := entry
		if entry.IsObject() {
			code = entry.Get("item")
		}
		codeStr, isStr := code.Str()
		hit, found := index[codeStr]
		if !isStr || !found {
			fe(key, fmt.Sprintf("%s「%s」不存在", catLabel, jsString(code)))
			continue
		}
		if dup[codeStr] {
			continue
		}
		dup[codeStr] = true
		itemName := jsString(hit.item.Get("name"))
		if hit.item.Get("disabled").Truthy() {
			fe(key, fmt.Sprintf("「%s」暂不可选", itemName))
			continue
		}
		cost, ok := itemCost(hit.item, mode)
		if !ok {
			if mode.Kind() == Null || mode.Kind() == Undefined {
				fe(key, "请先选择"+jsString(costByField.Get("label")))
			} else {
				modeLabel := Value{}
				if costBy.Truthy() {
					if opt, found := findOption(costByField, mode); found {
						modeLabel = opt.Get("label")
					}
				}
				if !modeLabel.Truthy() {
					modeLabel = mode
				}
				fe(key, fmt.Sprintf("「%s」不支持%s", itemName, jsString(modeLabel)))
			}
			continue
		}
		times := int64(1)
		if countVisible {
			var c Value
			if entry.IsObject() && entry.Get("count").IsDefined() {
				c = NumberValue(jsToNumber(entry.Get("count")))
			} else if def := count.Get("default"); def.Truthy() {
				c = def
			} else {
				c = count.Get("min")
			}
			minC, maxC := count.Get("min"), count.Get("max")
			if !c.IsInt() || c.n < minC.n || c.n > maxC.n {
				countLabel := "次数"
				if l := count.Get("label"); l.Truthy() {
					countLabel = jsString(l)
				}
				fe(key, fmt.Sprintf("「%s」%s需在 %s–%s 之间", itemName, countLabel, jsString(minC), jsString(maxC)))
				continue
			}
			times = int64(c.n)
		}
		list = append(list, objectValue([]string{"item", "count"}, map[string]Value{"item": code, "count": NumberValue(float64(times))}))
		steps = append(steps, Step{
			Label: itemName, Count: times, UnitPoints: cost, UnitLabel: "次",
			Ref: StepRef{Field: key, Item: codeStr, Group: hit.group, Mode: mode, isStage: true},
		})
		*breakdown = append(*breakdown, BreakdownLine{Label: itemName, Detail: fmt.Sprintf("%d 次 × %d", times, cost), Points: cost * times})
	}
	multiple := f.Get("multiple").Truthy()
	if !multiple && len(list) > 1 {
		fe(key, label+"只能选一个")
	}
	if maxSel := f.Get("max_select"); multiple && maxSel.Truthy() && float64(len(list)) > maxSel.n {
		fe(key, fmt.Sprintf("%s最多选 %s 个", label, jsString(maxSel)))
	}
	if f.Get("required").Truthy() && len(list) == 0 {
		fe(key, "请选择"+strings.TrimPrefix(label, "选择"))
	}
	values.set(key, ArrayValue(list))
	return steps
}

// PointsForUnits 返回已完成 unitsDone 个执行单位时应扣的积分：按步骤顺序累加原价再打折，
// 只要开始执行就收取固定部分；结果单调不减，全部完成时等于 Total。
func PointsForUnits(plan Plan, unitsDone int64) int64 {
	left := unitsDone
	if left > plan.Units {
		left = plan.Units
	}
	if left <= 0 {
		return 0
	}
	if left == plan.Units {
		return plan.Total
	}
	raw := plan.FixedPoints
	for _, s := range plan.Steps {
		if left <= 0 {
			break
		}
		take := s.Count
		if left < take {
			take = left
		}
		raw += take * s.UnitPoints
		left -= take
	}
	points := ApplyRate(raw, plan.Rate)
	if points > plan.Total {
		return plan.Total
	}
	return points
}

// StepProgress 返回每个步骤的完成数，供进度展示。
func StepProgress(plan Plan, unitsDone int64) []Step {
	left := unitsDone
	if left < 0 {
		left = 0
	}
	out := make([]Step, len(plan.Steps))
	for i, s := range plan.Steps {
		done := s.Count
		if left < done {
			done = left
		}
		left -= done
		s.Done = &done
		out[i] = s
	}
	return out
}
