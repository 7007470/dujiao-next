package featuredef

import (
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion 是当前支持的功能定义版本。
const SchemaVersion = 1

var (
	codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
	keyRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	itemRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_\-.]{0,31}$`)

	// FieldTypes 是支持的字段类型，顺序用于错误提示。
	FieldTypes = []string{"radio", "select", "checkbox", "switch", "number", "text", "stage_picker"}
	// RefundPolicies 是支持的退款策略。
	RefundPolicies = []string{"prorate", "full_on_system_fail", "none"}

	topKeys   = setOf("schema_version", "code", "name", "description", "capability", "timeout_minutes", "max_active_per_user", "refund_policy", "member_discount", "base_points", "catalogs", "fields", "limits")
	fieldKeys = setOf("key", "type", "label", "help", "required", "default", "visible_when", "options", "min_select", "max_select", "points", "min", "max", "unit", "points_per", "maxlength", "pattern", "placeholder", "catalog", "multiple", "cost_by", "count")
)

func setOf(items ...string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func isOptionType(t string) bool { return t == "radio" || t == "select" || t == "checkbox" }

// Issue 是一条校验错误或警告。Path 形如 "fields[1].options[0].value"。
type Issue struct {
	Path string `json:"path"`
	Msg  string `json:"msg"`
}

// Result 是 Validate 的结果。
type Result struct {
	OK       bool    `json:"ok"`
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

type validator struct {
	errors   []Issue
	warnings []Issue
	objKeys  int
}

// seenKey 模拟 JavaScript Map 的键：原始值按 === 比较，对象按引用（每个都不同）。
func (v *validator) seenKey(key Value) string {
	if k, ok := primitiveKey(key); ok {
		return k
	}
	v.objKeys++
	return fmt.Sprintf("o%d", v.objKeys)
}

func (v *validator) err(path, msg string)  { v.errors = append(v.errors, Issue{path, msg}) }
func (v *validator) warn(path, msg string) { v.warnings = append(v.warnings, Issue{path, msg}) }

func (v *validator) result() Result {
	errs, warns := v.errors, v.warnings
	if errs == nil {
		errs = []Issue{}
	}
	if warns == nil {
		warns = []Issue{}
	}
	return Result{OK: len(errs) == 0, Errors: errs, Warnings: warns}
}

// ValidateJSON 解析并校验功能定义。非法 JSON 返回一条 path 为空的错误。
func ValidateJSON(data []byte) Result {
	def, err := ParseValue(data)
	if err != nil {
		return Result{OK: false, Errors: []Issue{{"", "不是合法的 JSON"}}, Warnings: []Issue{}}
	}
	return Validate(def)
}

// Validate 校验功能定义，对应 catalog.js validateDefinition。
func Validate(def Value) Result {
	v := &validator{}
	if !def.IsObject() {
		v.err("", "功能定义必须是一个 JSON 对象")
		return v.result()
	}
	for _, k := range def.Keys() {
		if !topKeys[k] {
			v.warn(k, fmt.Sprintf("未知字段「%s」会被忽略", k))
		}
	}

	if sv := def.Get("schema_version"); sv.IsDefined() && !StrictEqual(sv, NumberValue(SchemaVersion)) {
		v.err("schema_version", fmt.Sprintf("只支持 schema_version = %d", SchemaVersion))
	}
	if s, ok := def.Get("code").Str(); !ok || !codeRe.MatchString(s) {
		v.err("code", "code 必须是 2–32 位小写字母、数字或下划线，并以字母开头")
	}
	if s, ok := def.Get("name").Str(); !ok || jsTrim(s) == "" || jsLen(s) > 40 {
		v.err("name", "name 必填，最多 40 个字")
	}
	if d := def.Get("description"); d.IsDefined() {
		if s, ok := d.Str(); !ok || jsLen(s) > 300 {
			v.err("description", "description 最多 300 个字")
		}
	}
	if c := def.Get("capability"); c.IsDefined() {
		if s, ok := c.Str(); !ok || !codeRe.MatchString(s) {
			v.err("capability", "capability 格式同 code")
		}
	}
	v.intRange(def, "timeout_minutes", 5, 1440, "")
	v.intRange(def, "max_active_per_user", 1, 20, "")
	v.intRange(def, "base_points", 0, 100000, "")
	if rp := def.Get("refund_policy"); rp.IsDefined() {
		if s, ok := rp.Str(); !ok || !contains(RefundPolicies, s) {
			v.err("refund_policy", "refund_policy 只能是 "+strings.Join(RefundPolicies, " / "))
		}
	}
	if md := def.Get("member_discount"); md.IsDefined() && md.Kind() != Bool {
		v.err("member_discount", "member_discount 必须是 true 或 false")
	}
	if limits := def.Get("limits"); limits.IsDefined() {
		if !limits.IsObject() {
			v.err("limits", "limits 必须是对象")
		} else {
			v.intRange(limits, "max_units", 1, 10000, "limits.")
		}
	}

	catalogs := def.Get("catalogs")
	if !catalogs.IsDefined() {
		catalogs = objectValue(nil, map[string]Value{})
	}
	if !catalogs.IsObject() {
		v.err("catalogs", "catalogs 必须是对象，键为目录名")
	} else {
		for _, ck := range catalogs.Keys() {
			v.validateCatalog(ck, catalogs.Get(ck))
		}
	}

	fields := def.Get("fields")
	if !fields.IsArray() || len(fields.Items()) == 0 {
		v.err("fields", "fields 必须是非空数组")
		return v.result()
	}
	if len(fields.Items()) > 30 {
		v.err("fields", "最多 30 个字段")
	}
	seen := map[string]Value{}
	for i, f := range fields.Items() {
		v.validateField(fmt.Sprintf("fields[%d]", i), f, catalogs, seen)
	}
	return v.result()
}

func (v *validator) validateCatalog(ck string, cat Value) {
	p := "catalogs." + ck
	if !keyRe.MatchString(ck) {
		v.err(p, fmt.Sprintf("目录名「%s」格式不正确", ck))
	}
	if !cat.IsObject() {
		v.err(p, "目录必须是对象")
		return
	}
	if s, ok := cat.Get("label").Str(); !ok || jsTrim(s) == "" {
		v.err(p+".label", `目录需要 label，例如"关卡"`)
	}
	groups := cat.Get("groups")
	if !groups.IsArray() || len(groups.Items()) == 0 {
		v.err(p+".groups", "groups 必须是非空数组（例如按章节分组）")
		return
	}
	groupCodes := valueSet{}
	itemCodes := valueSet{}
	for gi, g := range groups.Items() {
		gp := fmt.Sprintf("%s.groups[%d]", p, gi)
		if !g.IsObject() {
			v.err(gp, "分组必须是对象")
			continue
		}
		code := g.Get("code")
		if s, ok := code.Str(); !ok || !itemRe.MatchString(s) {
			v.err(gp+".code", "分组 code 必填，字母数字")
		} else if groupCodes.has(code) {
			v.err(gp+".code", fmt.Sprintf("分组 code「%s」重复", s))
		}
		groupCodes.add(code)
		if s, ok := g.Get("name").Str(); !ok || jsTrim(s) == "" {
			v.err(gp+".name", "分组 name 必填")
		}
		items := g.Get("items")
		if !items.IsArray() || len(items.Items()) == 0 {
			v.err(gp+".items", "items 必须是非空数组")
			continue
		}
		for ii, it := range items.Items() {
			v.validateItem(fmt.Sprintf("%s.items[%d]", gp, ii), it, itemCodes)
		}
	}
}

func (v *validator) validateItem(ip string, it Value, itemCodes valueSet) {
	if !it.IsObject() {
		v.err(ip, "条目必须是对象")
		return
	}
	code := it.Get("code")
	if s, ok := code.Str(); !ok || !itemRe.MatchString(s) {
		v.err(ip+".code", "条目 code 必填，字母、数字、- _ .")
	} else if itemCodes.has(code) {
		v.err(ip+".code", fmt.Sprintf("条目 code「%s」在目录内重复", s))
	}
	itemCodes.add(code)
	if s, ok := it.Get("name").Str(); !ok || jsTrim(s) == "" {
		v.err(ip+".name", "条目 name 必填")
	}
	cost := it.Get("cost")
	switch {
	case cost.IsInt():
		if cost.n < 0 {
			v.err(ip+".cost", "cost 不能为负")
		}
	case cost.IsObject():
		if len(cost.Keys()) == 0 {
			v.err(ip+".cost", "cost 对象不能为空")
		}
		for _, mk := range cost.Keys() {
			if mv := cost.Get(mk); !mv.IsInt() || mv.n < 0 {
				v.err(fmt.Sprintf("%s.cost.%s", ip, mk), "cost 必须是非负整数")
			}
		}
	default:
		v.err(ip+".cost", `cost 必须是整数，或按模式区分的对象，例如 {"clear": 8, "sweep": 3}`)
	}
	if tags := it.Get("tags"); tags.IsDefined() {
		bad := !tags.IsArray()
		for _, t := range tags.Items() {
			if t.Kind() != String {
				bad = true
			}
		}
		if bad {
			v.err(ip+".tags", "tags 必须是字符串数组")
		}
	}
	if d := it.Get("disabled"); d.IsDefined() && d.Kind() != Bool {
		v.err(ip+".disabled", "disabled 必须是布尔值")
	}
}

func (v *validator) validateField(p string, f Value, catalogs Value, seen map[string]Value) {
	if !f.IsObject() {
		v.err(p, "字段必须是对象")
		return
	}
	for _, k := range f.Keys() {
		if !fieldKeys[k] {
			v.warn(p+"."+k, fmt.Sprintf("未知属性「%s」会被忽略", k))
		}
	}
	keyValue := f.Get("key")
	key := v.seenKey(keyValue)
	if s, ok := keyValue.Str(); !ok || !keyRe.MatchString(s) {
		v.err(p+".key", "key 必须是小写字母开头的字母、数字、下划线")
	} else if _, dup := seen[key]; dup {
		v.err(p+".key", fmt.Sprintf("key「%s」重复", s))
	}
	fieldType, _ := f.Get("type").Str()
	if f.Get("type").Kind() != String || !contains(FieldTypes, fieldType) {
		v.err(p+".type", "type 只能是 "+strings.Join(FieldTypes, " / "))
		seen[key] = f
		return
	}
	if s, ok := f.Get("label").Str(); !ok || jsTrim(s) == "" || jsLen(s) > 30 {
		v.err(p+".label", "label 必填，最多 30 个字")
	}
	if r := f.Get("required"); r.IsDefined() && r.Kind() != Bool {
		v.err(p+".required", "required 必须是布尔值")
	}

	if isOptionType(fieldType) {
		v.validateOptions(p, f, fieldType)
	}
	switch fieldType {
	case "switch":
		if pts := f.Get("points"); pts.IsDefined() && (!pts.IsInt() || pts.n < 0) {
			v.err(p+".points", "points 必须是非负整数")
		}
		if d := f.Get("default"); d.IsDefined() && d.Kind() != Bool {
			v.err(p+".default", "default 必须是布尔值")
		}
	case "number":
		minV, maxV := f.Get("min"), f.Get("max")
		switch {
		case !minV.IsInt() || !maxV.IsInt():
			v.err(p, "number 需要整数 min 和 max")
		case minV.n > maxV.n:
			v.err(p, "min 不能大于 max")
		default:
			if d := f.Get("default"); d.IsDefined() && (!d.IsInt() || d.n < minV.n || d.n > maxV.n) {
				v.err(p+".default", fmt.Sprintf("default 必须在 %s–%s 之间", jsString(minV), jsString(maxV)))
			}
		}
		if pp := f.Get("points_per"); pp.IsDefined() && (!pp.IsInt() || pp.n < 0) {
			v.err(p+".points_per", "points_per 必须是非负整数")
		}
		if u := f.Get("unit"); u.IsDefined() {
			if s, ok := u.Str(); !ok || jsLen(s) > 8 {
				v.err(p+".unit", "unit 最多 8 个字")
			}
		}
	case "text":
		v.intRange(f, "maxlength", 1, 200, p+".")
		if pat := f.Get("pattern"); pat.IsDefined() {
			if _, err := compilePattern(jsString(pat)); err != nil {
				v.err(p+".pattern", "pattern 不是合法的正则表达式")
			}
		}
	case "stage_picker":
		v.validateStagePicker(p, f, catalogs, seen)
	}
	if vw := f.Get("visible_when"); vw.IsDefined() {
		v.checkVisibleWhen(vw, p+".visible_when", seen)
	}
	seen[key] = f
}

func (v *validator) validateOptions(p string, f Value, fieldType string) {
	options := f.Get("options")
	if !options.IsArray() || len(options.Items()) == 0 {
		v.err(p+".options", "需要非空 options")
	} else {
		vals := valueSet{}
		for oi, o := range options.Items() {
			op := fmt.Sprintf("%s.options[%d]", p, oi)
			if !o.IsObject() {
				v.err(op, "选项必须是对象 {value, label}")
				continue
			}
			value := o.Get("value")
			if s, ok := value.Str(); !ok || jsTrim(s) == "" || jsLen(s) > 32 {
				v.err(op+".value", "value 必须是非空字符串")
			} else if vals.has(value) {
				v.err(op+".value", fmt.Sprintf("value「%s」重复", s))
			}
			vals.add(value)
			if s, ok := o.Get("label").Str(); !ok || jsTrim(s) == "" {
				v.err(op+".label", "label 必填")
			}
			if pts := o.Get("points"); pts.IsDefined() && (!pts.IsInt() || pts.n < 0) {
				v.err(op+".points", "points 必须是非负整数")
			}
		}
		if d := f.Get("default"); d.IsDefined() {
			defs := []Value{d}
			ok := true
			if fieldType == "checkbox" {
				ok = d.IsArray()
				defs = d.Items()
			}
			for _, x := range defs {
				if !vals.has(x) {
					ok = false
				}
			}
			if !ok {
				v.err(p+".default", "default 必须是 options 里的 value")
			}
		}
	}
	if fieldType == "checkbox" {
		v.intRange(f, "min_select", 0, 50, p+".")
		v.intRange(f, "max_select", 1, 50, p+".")
	}
}

func (v *validator) validateStagePicker(p string, f Value, catalogs Value, seen map[string]Value) {
	catalogName, isStr := f.Get("catalog").Str()
	var cat Value
	if catalogs.IsObject() {
		cat = catalogs.Get(jsString(f.Get("catalog"))) // 与 catalogs[f.catalog] 一致，键按字符串取
	}
	if !isStr || !cat.Truthy() {
		suffix := ""
		if isStr {
			suffix = fmt.Sprintf("（找不到「%s」）", catalogName)
		}
		v.err(p+".catalog", "catalog 必须指向 catalogs 里的目录"+suffix)
	}
	multiple := f.Get("multiple")
	if multiple.IsDefined() && multiple.Kind() != Bool {
		v.err(p+".multiple", "multiple 必须是布尔值")
	}
	if multiple.Truthy() {
		v.intRange(f, "max_select", 1, 200, p+".")
	}
	if count := f.Get("count"); count.IsDefined() {
		if !count.IsObject() {
			v.err(p+".count", "count 必须是对象 {label, min, max, default}")
		} else {
			minV, maxV := count.Get("min"), count.Get("max")
			if !minV.IsInt() || !maxV.IsInt() || minV.n < 1 || minV.n > maxV.n || maxV.n > 999 {
				v.err(p+".count", "count 需要整数 min ≥ 1、max ≤ 999，且 min ≤ max")
			}
			if d := count.Get("default"); d.IsDefined() && (!d.IsInt() || jsLess(d, minV) || jsLess(maxV, d)) {
				v.err(p+".count.default", "count.default 超出范围")
			}
			if vw := count.Get("visible_when"); vw.IsDefined() {
				v.checkVisibleWhen(vw, p+".count.visible_when", seen)
			}
		}
	}
	groups := cat.Get("groups")
	if costBy := f.Get("cost_by"); costBy.IsDefined() {
		ref, found := seen[v.seenKey(costBy)]
		refType, _ := ref.Get("type").Str()
		if !found || !(refType == "radio" || refType == "select") {
			v.err(p+".cost_by", "cost_by 必须引用前面的 radio 或 select 字段")
		} else if cat.Truthy() && groups.IsArray() {
			modes := valueSet{}
			for _, o := range ref.Get("options").Items() {
				modes.add(o.Get("value"))
			}
			for gi, g := range groups.Items() {
				for ii, it := range g.Get("items").Items() {
					cost := it.Get("cost")
					if !cost.IsObject() {
						continue
					}
					for _, mk := range cost.Keys() {
						if !modes.has(StringValue(mk)) {
							v.err(fmt.Sprintf("catalogs.%s.groups[%d].items[%d].cost.%s", jsString(f.Get("catalog")), gi, ii, mk),
								fmt.Sprintf("「%s」不是 %s 的选项", mk, jsString(costBy)))
						}
					}
				}
			}
		}
	} else if cat.Truthy() && groups.IsArray() && anyModeCost(groups) {
		v.err(p+".cost_by", "目录里有按模式区分的 cost，需要用 cost_by 指定模式字段")
	}
}

func anyModeCost(groups Value) bool {
	for _, g := range groups.Items() {
		for _, it := range g.Get("items").Items() {
			if it.Get("cost").IsObject() {
				return true
			}
		}
	}
	return false
}

// jsLess 对应 a < b（这里两侧都是数字或 undefined，undefined 参与比较结果为 false）。
func jsLess(a, b Value) bool {
	x, y := jsToNumber(a), jsToNumber(b)
	return x < y
}

func (v *validator) checkVisibleWhen(vw Value, path string, seen map[string]Value) {
	if !vw.IsObject() || len(vw.Keys()) == 0 {
		v.err(path, `visible_when 必须是对象，例如 {"mode": "sweep"}`)
		return
	}
	for _, k := range vw.Keys() {
		ref, found := seen[v.seenKey(StringValue(k))]
		if !found {
			v.err(path+"."+k, fmt.Sprintf("只能引用排在前面的字段，找不到「%s」", k))
			continue
		}
		raw := vw.Get(k)
		vals := []Value{raw}
		if raw.IsArray() {
			vals = raw.Items()
		}
		refType, _ := ref.Get("type").Str()
		switch {
		case isOptionType(refType) && refType != "checkbox":
			ok := valueSet{}
			for _, o := range ref.Get("options").Items() {
				ok.add(o.Get("value"))
			}
			for _, x := range vals {
				if !ok.has(x) {
					v.err(path+"."+k, fmt.Sprintf("「%s」不是 %s 的选项", jsString(x), k))
				}
			}
		case refType == "switch":
			for _, x := range vals {
				if x.Kind() != Bool {
					v.err(path+"."+k, "switch 条件只能是 true 或 false")
					break
				}
			}
		default:
			v.err(path+"."+k, "visible_when 只能引用 radio、select 或 switch 字段")
		}
	}
}

func (v *validator) intRange(obj Value, key string, minV, maxV float64, prefix string) {
	value := obj.Get(key)
	if !value.IsDefined() {
		return
	}
	if !value.IsInt() || value.n < minV || value.n > maxV {
		v.err(prefix+key, fmt.Sprintf("%s 必须是 %s–%s 之间的整数", key, jsNumberString(minV), jsNumberString(maxV)))
	}
}

// compilePattern 编译 text 字段的 pattern（Go RE2 语法，不支持反向引用和环视）。
func compilePattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
