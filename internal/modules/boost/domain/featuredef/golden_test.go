package featuredef

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// golden.json 由 prototype/tools/gen-featuredef-golden.js 用原型 catalog.js 生成：
// 示例定义、catalog.test.js 的场景、随机改坏的定义和随机参数。Go 的结果必须与 JS 逐字段一致。
type goldenFile struct {
	Validate []struct {
		Name string          `json:"name"`
		Def  json.RawMessage `json:"def"`
		Want json.RawMessage `json:"want"`
	} `json:"validate"`
	Compile []struct {
		Name   string          `json:"name"`
		Def    json.RawMessage `json:"def"`
		Params json.RawMessage `json:"params"`
		Rate   *int            `json:"rate"`
		Want   struct {
			Plan     json.RawMessage `json:"plan"`
			Settle   []int64         `json:"settle"`
			Progress []struct {
				Units int64   `json:"units"`
				Steps []int64 `json:"steps"`
			} `json:"progress"`
		} `json:"want"`
	} `json:"compile"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden goldenFile
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(golden.Validate) < 200 || len(golden.Compile) < 300 {
		t.Fatalf("golden too small: validate=%d compile=%d", len(golden.Validate), len(golden.Compile))
	}
	return golden
}

// parseRaw 把缺省的 JSON 字段（def 为 undefined 时 JSON.stringify 会省略）当作 Undefined。
func parseRaw(t *testing.T, raw json.RawMessage) Value {
	t.Helper()
	if len(raw) == 0 {
		return Value{}
	}
	v, err := ParseValue(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return v
}

// canonical 把 JSON 重新排版成可比较的形式（对象键顺序保留，用 Value 往返）。
func canonical(t *testing.T, data []byte) string {
	t.Helper()
	v, err := ParseValue(data)
	if err != nil {
		t.Fatalf("canonical parse %s: %v", data, err)
	}
	out, _ := v.MarshalJSON()
	return string(out)
}

func TestGoldenValidateMatchesPrototype(t *testing.T) {
	golden := loadGolden(t)
	for _, c := range golden.Validate {
		got, err := json.Marshal(Validate(parseRaw(t, c.Def)))
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.Name, err)
		}
		if want := canonical(t, c.Want); canonical(t, got) != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.Name, got, want)
		}
	}
}

func TestGoldenCompileMatchesPrototype(t *testing.T) {
	golden := loadGolden(t)
	for _, c := range golden.Compile {
		def := parseRaw(t, c.Def)
		if r := Validate(def); !r.OK {
			t.Fatalf("%s: golden def is invalid in Go: %+v", c.Name, r.Errors)
		}
		rate := 0
		if c.Rate != nil {
			rate = *c.Rate
		}
		plan := Compile(def, parseRaw(t, c.Params), rate)
		got, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.Name, err)
		}
		if want := canonical(t, c.Want.Plan); canonical(t, got) != want {
			t.Errorf("%s plan\n got: %s\nwant: %s", c.Name, got, want)
			continue
		}
		settle := make([]int64, 0, len(c.Want.Settle))
		for u := int64(0); u <= plan.Units+1; u++ {
			settle = append(settle, PointsForUnits(plan, u))
		}
		if !reflect.DeepEqual(settle, c.Want.Settle) {
			t.Errorf("%s settle got %v want %v", c.Name, settle, c.Want.Settle)
		}
		for _, p := range c.Want.Progress {
			var done []int64
			for _, s := range StepProgress(plan, p.Units) {
				done = append(done, *s.Done)
			}
			if !reflect.DeepEqual(done, p.Steps) {
				t.Errorf("%s progress(%d) got %v want %v", c.Name, p.Units, done, p.Steps)
			}
		}
	}
}

func TestValueRoundTripKeepsKeyOrderAndJSKeyRules(t *testing.T) {
	v, err := ParseValue([]byte(`{"b":1,"a":2,"10":3,"2":4,"b":5}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := v.MarshalJSON()
	if !bytes.Equal(out, []byte(`{"2":4,"10":3,"b":5,"a":2}`)) {
		t.Fatalf("got %s", out)
	}
	for _, bad := range []string{``, `{`, `{"a":1} x`, `[1,]`} {
		if _, err := ParseValue([]byte(bad)); err == nil {
			t.Errorf("ParseValue(%q) succeeded, want error", bad)
		}
	}
}

func TestJSNumberConversion(t *testing.T) {
	cases := map[string]string{
		`" 60 "`: "60", `"0x10"`: "16", `""`: "0", `"12.5"`: "12.5", `"abc"`: "NaN", `true`: "1",
		`null`: "0", `[]`: "0", `[7]`: "7", `[1,2]`: "NaN", `{}`: "NaN", `"1e3"`: "1000", `"-0"`: "0",
	}
	for in, want := range cases {
		if got := jsNumberString(jsToNumber(parseRaw(t, json.RawMessage(in)))); got != want {
			t.Errorf("Number(%s) = %s, want %s", in, got, want)
		}
	}
	for f, want := range map[float64]string{0.1: "0.1", 1e21: "1e+21", 123456789012: "123456789012", 1e-7: "1e-7", 0.000001: "0.000001", 2.5: "2.5"} {
		if got := jsNumberString(f); got != want {
			t.Errorf("String(%v) = %s, want %s", f, got, want)
		}
	}
}
