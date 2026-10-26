// Package featuredef 实现代练功能定义 JSON 规范（docs/boost-feature-schema.md）：
// 校验（Validate）、补全默认值（Parse / Normalize）、编译报价（Compile）和按完成量结算（PointsForUnits）。
//
// 这是唯一的一份实现：后台保存、用户报价和服务端下单都调用这里。行为与原型
// prototype/src/catalog.js 逐条一致（包括 JavaScript 的类型转换细节），由 testdata 里的
// JS/Go 对照用例保证；唯一的已知差异是 text 字段的 pattern 按 Go RE2 语法解释。
package featuredef

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind 是 JSON 值的类型。Undefined 表示键不存在（区别于 null）。
type Kind uint8

const (
	Undefined Kind = iota
	Null
	Bool
	Number
	String
	Array
	Object
)

// Value 是按 JavaScript 语义处理的 JSON 值：保留对象键顺序，区分"缺失"和 null。
type Value struct {
	kind  Kind
	b     bool
	n     float64
	s     string
	arr   []Value
	keys  []string
	props map[string]Value
}

// StringValue 等构造函数用于生成编译结果里的值。
func StringValue(s string) Value  { return Value{kind: String, s: s} }
func NumberValue(n float64) Value { return Value{kind: Number, n: n + 0} } // +0 把 -0 变成 0
func BoolValue(b bool) Value      { return Value{kind: Bool, b: b} }
func ArrayValue(items []Value) Value {
	return Value{kind: Array, arr: append([]Value{}, items...)}
}

// objectValue 按给定顺序构造对象。
func objectValue(keys []string, props map[string]Value) Value {
	return Value{kind: Object, keys: keys, props: props}
}

func (v Value) Kind() Kind      { return v.kind }
func (v Value) IsObject() bool  { return v.kind == Object }
func (v Value) IsArray() bool   { return v.kind == Array }
func (v Value) IsDefined() bool { return v.kind != Undefined }

// Get 取对象属性；不是对象或没有该键时返回 Undefined。
func (v Value) Get(key string) Value {
	if v.kind != Object {
		return Value{}
	}
	return v.props[key]
}

// Keys 返回对象的键，顺序与 JavaScript Object.keys 一致（数组下标形式的键升序在前，其余按出现顺序）。
func (v Value) Keys() []string {
	if v.kind != Object {
		return nil
	}
	return v.keys
}

// Items 返回数组元素；不是数组时返回 nil。
func (v Value) Items() []Value {
	if v.kind != Array {
		return nil
	}
	return v.arr
}

// Str 在值是字符串时返回它。
func (v Value) Str() (string, bool) { return v.s, v.kind == String }

// Num 在值是数字时返回它。
func (v Value) Num() (float64, bool) { return v.n, v.kind == Number }

// IsInt 对应 Number.isInteger。
func (v Value) IsInt() bool {
	return v.kind == Number && !math.IsInf(v.n, 0) && v.n == math.Trunc(v.n)
}

// Truthy 对应 JavaScript 的真值判断。
func (v Value) Truthy() bool {
	switch v.kind {
	case Undefined, Null:
		return false
	case Bool:
		return v.b
	case Number:
		return v.n != 0 && !math.IsNaN(v.n)
	case String:
		return v.s != ""
	default:
		return true
	}
}

// StrictEqual 对应 ===（数组和对象按引用比较，两个独立解析的值永远不相等）。
func StrictEqual(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case Undefined, Null:
		return true
	case Bool:
		return a.b == b.b
	case Number:
		return a.n == b.n
	case String:
		return a.s == b.s
	default:
		return false
	}
}

// valueSet 是按 === 判断成员的集合（对应 JavaScript 的 Set / includes）。
type valueSet map[string]struct{}

func primitiveKey(v Value) (string, bool) {
	switch v.kind {
	case Undefined:
		return "u", true
	case Null:
		return "z", true
	case Bool:
		return "b" + strconv.FormatBool(v.b), true
	case Number:
		return "n" + strconv.FormatFloat(v.n, 'g', -1, 64), true
	case String:
		return "s" + v.s, true
	default:
		return "", false
	}
}

func (s valueSet) add(v Value) {
	if key, ok := primitiveKey(v); ok {
		s[key] = struct{}{}
	}
}

func (s valueSet) has(v Value) bool {
	key, ok := primitiveKey(v)
	if !ok {
		return false
	}
	_, found := s[key]
	return found
}

// ---------------------------------------------------------------------------
// JSON 解析和输出
// ---------------------------------------------------------------------------

// ErrInvalidJSON 表示输入不是合法 JSON。
var ErrInvalidJSON = errors.New("featuredef: invalid json")

// ParseValue 解析 JSON。重复的键以最后一次为准、位置保留第一次（与 JSON.parse 一致）。
func ParseValue(data []byte) (Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return Value{}, ErrInvalidJSON
	}
	// 末尾只能有空白（decoder.Token 对非法尾部会报错而不是 io.EOF，不能用它判断）
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) > 0 {
		return Value{}, ErrInvalidJSON
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (Value, error) {
	token, err := decoder.Token()
	if err != nil {
		return Value{}, err
	}
	switch t := token.(type) {
	case nil:
		return Value{kind: Null}, nil
	case bool:
		return BoolValue(t), nil
	case string:
		return StringValue(t), nil
	case json.Number:
		n, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return Value{}, err
		}
		return NumberValue(n), nil
	case json.Delim:
		if t == '[' {
			items := []Value{}
			for decoder.More() {
				item, err := decodeValue(decoder)
				if err != nil {
					return Value{}, err
				}
				items = append(items, item)
			}
			if _, err := decoder.Token(); err != nil {
				return Value{}, err
			}
			return Value{kind: Array, arr: items}, nil
		}
		var keys []string
		props := map[string]Value{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return Value{}, err
			}
			key, _ := keyToken.(string)
			item, err := decodeValue(decoder)
			if err != nil {
				return Value{}, err
			}
			if _, exists := props[key]; !exists {
				keys = append(keys, key)
			}
			props[key] = item
		}
		if _, err := decoder.Token(); err != nil {
			return Value{}, err
		}
		return objectValue(jsKeyOrder(keys), props), nil
	}
	return Value{}, ErrInvalidJSON
}

// jsKeyOrder 把数组下标形式的键（"0"、"12"）升序排在前面，与 JavaScript 对象属性顺序一致。
func jsKeyOrder(keys []string) []string {
	var indexes, others []string
	for _, key := range keys {
		if isArrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	if len(indexes) == 0 {
		return keys
	}
	sort.Slice(indexes, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexes[i], 10, 64)
		b, _ := strconv.ParseUint(indexes[j], 10, 64)
		return a < b
	})
	return append(indexes, others...)
}

func isArrayIndex(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n < 4294967295
}

// MarshalJSON 按 JSON.stringify 输出：对象里的 Undefined 省略，数组里的 Undefined 和非有限数字输出 null。
func (v Value) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	writeValue(&buf, v)
	return buf.Bytes(), nil
}

func writeValue(buf *bytes.Buffer, v Value) {
	switch v.kind {
	case Undefined, Null:
		buf.WriteString("null")
	case Bool:
		buf.WriteString(strconv.FormatBool(v.b))
	case Number:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			buf.WriteString("null")
		} else {
			buf.WriteString(jsNumberString(v.n))
		}
	case String:
		encoded, _ := json.Marshal(v.s)
		buf.Write(encoded)
	case Array:
		buf.WriteByte('[')
		for i, item := range v.arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeValue(buf, item)
		}
		buf.WriteByte(']')
	case Object:
		buf.WriteByte('{')
		first := true
		for _, key := range v.keys {
			item := v.props[key]
			if item.kind == Undefined {
				continue
			}
			if !first {
				buf.WriteByte(',')
			}
			first = false
			encoded, _ := json.Marshal(key)
			buf.Write(encoded)
			buf.WriteByte(':')
			writeValue(buf, item)
		}
		buf.WriteByte('}')
	}
}

// ---------------------------------------------------------------------------
// JavaScript 类型转换
// ---------------------------------------------------------------------------

// jsString 对应 String(v)。
func jsString(v Value) string {
	switch v.kind {
	case Undefined:
		return "undefined"
	case Null:
		return "null"
	case Bool:
		return strconv.FormatBool(v.b)
	case Number:
		return jsNumberString(v.n)
	case String:
		return v.s
	case Array:
		parts := make([]string, len(v.arr))
		for i, item := range v.arr {
			if item.kind != Undefined && item.kind != Null {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// jsNumberString 对应 Number.prototype.toString()。
func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	case f < 0:
		return "-" + jsNumberString(-f)
	}
	// 最短可还原的十进制位数和指数，再按 ECMAScript Number::toString 的规则排版
	scientific := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(scientific, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(exponent)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return "0." + strings.Repeat("0", -n) + digits
	}
	e := n - 1
	sign := "+"
	if e < 0 {
		sign, e = "-", -e
	}
	if k == 1 {
		return digits + "e" + sign + strconv.Itoa(e)
	}
	return digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
}

var (
	jsDecimalRe = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
	jsHexRe     = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	jsOctalRe   = regexp.MustCompile(`^0[oO][0-7]+$`)
	jsBinaryRe  = regexp.MustCompile(`^0[bB][01]+$`)
)

// jsToNumber 对应 Number(v)。
func jsToNumber(v Value) float64 {
	switch v.kind {
	case Undefined:
		return math.NaN()
	case Null:
		return 0
	case Bool:
		if v.b {
			return 1
		}
		return 0
	case Number:
		return v.n
	case String:
		return jsStringToNumber(v.s)
	default:
		return jsStringToNumber(jsString(v))
	}
}

func jsStringToNumber(raw string) float64 {
	s := jsTrim(raw)
	switch {
	case s == "":
		return 0
	case s == "Infinity" || s == "+Infinity":
		return math.Inf(1)
	case s == "-Infinity":
		return math.Inf(-1)
	case jsHexRe.MatchString(s):
		return parseRadix(s[2:], 16)
	case jsOctalRe.MatchString(s):
		return parseRadix(s[2:], 8)
	case jsBinaryRe.MatchString(s):
		return parseRadix(s[2:], 2)
	case jsDecimalRe.MatchString(s):
		n, err := strconv.ParseFloat(s, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return math.NaN()
		}
		return n
	}
	return math.NaN()
}

func parseRadix(digits string, base float64) float64 {
	result := 0.0
	for _, r := range digits {
		d, _ := strconv.ParseInt(string(r), int(base), 64)
		result = result*base + float64(d)
	}
	return result
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// jsTrim 对应 String.prototype.trim。
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsLen 对应 String.prototype.length（UTF-16 码元数）。
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	if !utf8.ValidString(s) {
		return utf8.RuneCountInString(s)
	}
	return n
}
