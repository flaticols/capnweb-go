package capnweb

import (
	"encoding/json"
	"math/big"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// jsonEq reports whether two JSON byte slices are semantically equal,
// ignoring key order and insignificant whitespace.
func jsonEq(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("invalid JSON %q: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// goldenExprs are the canonical wire forms for every expression type. These
// vectors are derived from the cloudflare/capnweb reference (src/serialize.ts,
// protocol.md) and lock down byte-level compatibility, with emphasis on the
// 0.8.0 additions: blob, error props (with null stack slot), and invalid date.
var goldenExprs = []struct {
	name string
	expr Expr
	wire string
}{
	// Literals & specials.
	{"undefined", UndefinedExpr{}, `["undefined"]`},
	{"inf", InfExpr{}, `["inf"]`},
	{"-inf", NegInfExpr{}, `["-inf"]`},
	{"nan", NaNExpr{}, `["nan"]`},

	// Data types.
	{"bytes", BytesExpr{Data: []byte("hi")}, `["bytes","aGk"]`},
	{"bigint", BigIntExpr{Value: big.NewInt(9007199254740993)}, `["bigint","9007199254740993"]`},
	{"date", DateExpr{Time: time.UnixMilli(1718200000000)}, `["date",1718200000000]`},
	{"date_invalid", DateExpr{}, `["date",null]`},

	// Error: legacy 3-element, 4-element with stack, and 0.8.0 props forms.
	{"error_basic", ErrorExpr{Type: "Error", Message: "boom"}, `["error","Error","boom"]`},
	{"error_stack", ErrorExpr{Type: "TypeError", Message: "bad", Stack: "at x"}, `["error","TypeError","bad","at x"]`},
	{
		"error_props_null_stack",
		ErrorExpr{Type: "TypeError", Message: "bad", Props: map[string]Expr{"code": LiteralExpr{Value: float64(42)}}},
		`["error","TypeError","bad",null,{"code":42}]`,
	},
	{
		"error_props_with_stack",
		ErrorExpr{Type: "Error", Message: "x", Stack: "at y", Props: map[string]Expr{"detail": LiteralExpr{Value: "z"}}},
		`["error","Error","x","at y",{"detail":"z"}]`,
	},

	// Blob (0.8.0): bytes ride on a readable-stream expression.
	{"blob", BlobExpr{Type: "text/plain", Body: ReadableExpr{ImportID: 5}}, `["blob","text/plain",["readable",5]]`},

	// Arrays are escaped by wrapping in a one-element outer array.
	{"array", ArrayExpr{Elements: []Expr{LiteralExpr{Value: "a"}, LiteralExpr{Value: float64(1)}}}, `[["a",1]]`},
	{"array_empty", ArrayExpr{Elements: []Expr{}}, `[[]]`},
	{"array_nested", ArrayExpr{Elements: []Expr{ArrayExpr{Elements: []Expr{LiteralExpr{Value: float64(1)}}}}}, `[[[[1]]]]`},
	// Objects recurse into their property values (nested array gets escaped).
	{"object_nested_array", ObjectExpr{Fields: map[string]Expr{"k": ArrayExpr{Elements: []Expr{LiteralExpr{Value: float64(1)}, LiteralExpr{Value: float64(2)}}}}}, `{"k":[[1,2]]}`},
	{"object_nested_date", ObjectExpr{Fields: map[string]Expr{"d": DateExpr{Time: time.UnixMilli(1000)}}}, `{"d":["date",1000]}`},

	// References.
	{"export", ExportExpr{ExportID: 3}, `["export",3]`},
	{"promise", PromiseExpr{ExportID: 7}, `["promise",7]`},
	{"writable", WritableExpr{ExportID: 2}, `["writable",2]`},
	{"readable", ReadableExpr{ImportID: 4}, `["readable",4]`},
	{"import_bare", ImportExpr{ImportID: 1}, `["import",1]`},
	{"import_call", ImportExpr{ImportID: 1, Path: []string{"add"}, Args: []Expr{LiteralExpr{Value: float64(1)}, LiteralExpr{Value: float64(2)}}}, `["import",1,"add",[1,2]]`},
	{"pipeline_call", PipelineExpr{ImportID: 2, Path: []string{"foo"}, Args: []Expr{}}, `["pipeline",2,"foo",[]]`},

	// HTTP types.
	{"headers", HeadersExpr{Header: http.Header{"X-A": {"1"}}}, `["headers",[["X-A","1"]]]`},
	// Duplicate values for a field are comma-combined into one pair.
	{"headers_multi", HeadersExpr{Header: http.Header{"X-A": {"1", "2"}}}, `["headers",[["X-A","1, 2"]]]`},

	// Remap.
	{
		"remap",
		RemapExpr{ImportID: 1, Path: []string{}, Captures: []Expr{}, Instructions: []Expr{ImportExpr{ImportID: 0, Path: []string{"x"}, Args: []Expr{}}}},
		`["remap",1,[],[],[["import",0,"x",[]]]]`,
	},
}

func TestGoldenExprEncode(t *testing.T) {
	for _, tc := range goldenExprs {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeExpr(tc.expr)
			if err != nil {
				t.Fatalf("EncodeExpr(%s): %v", tc.name, err)
			}
			if !jsonEq(t, got, []byte(tc.wire)) {
				t.Errorf("EncodeExpr(%s) = %s, want %s", tc.name, got, tc.wire)
			}
		})
	}
}

// TestGoldenExprRoundTrip decodes each canonical wire form and re-encodes it,
// asserting the wire form is reproduced (stability of decode∘encode).
func TestGoldenExprRoundTrip(t *testing.T) {
	for _, tc := range goldenExprs {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := DecodeExpr([]byte(tc.wire))
			if err != nil {
				t.Fatalf("DecodeExpr(%s): %v", tc.wire, err)
			}
			reencoded, err := EncodeExpr(decoded)
			if err != nil {
				t.Fatalf("EncodeExpr after decode (%s): %v", tc.name, err)
			}
			if !jsonEq(t, reencoded, []byte(tc.wire)) {
				t.Errorf("round-trip(%s) = %s, want %s", tc.name, reencoded, tc.wire)
			}
		})
	}
}

// TestStrictDecodeRejects asserts that malformed/unescaped expressions are
// rejected as the reference does (throw "unknown special value"), rather than
// being silently accepted as plain arrays.
func TestStrictDecodeRejects(t *testing.T) {
	bad := []string{
		`[1,2,3]`,           // bare (unescaped) array
		`["foo",1]`,         // unknown string tag
		`[]`,                // empty unescaped array
		`[5]`,               // single non-array element, unknown tag
		`["undefined","x"]`, // over-length undefined
		`["pipe"]`,          // message tag, not a valid expression
	}
	for _, w := range bad {
		t.Run(w, func(t *testing.T) {
			if _, err := DecodeExpr([]byte(w)); err == nil {
				t.Errorf("DecodeExpr(%s) = nil error; want rejection", w)
			}
		})
	}
}

// TestEscapedArrayDecode confirms the escape rule: [[...]] -> the inner array.
func TestEscapedArrayDecode(t *testing.T) {
	got, err := DecodeExpr([]byte(`[[1,2,3]]`))
	if err != nil {
		t.Fatalf("decode escaped array: %v", err)
	}
	arr, ok := got.(ArrayExpr)
	if !ok {
		t.Fatalf("expected ArrayExpr, got %T", got)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
}

func TestGoldenDateInvalidDecode(t *testing.T) {
	decoded, err := DecodeExpr([]byte(`["date",null]`))
	if err != nil {
		t.Fatalf("decode invalid date: %v", err)
	}
	d, ok := decoded.(DateExpr)
	if !ok {
		t.Fatalf("expected DateExpr, got %T", decoded)
	}
	if !d.Time.IsZero() {
		t.Errorf("invalid date decoded to %v, want zero time", d.Time)
	}
}

func TestGoldenErrorPropsDecode(t *testing.T) {
	decoded, err := DecodeExpr([]byte(`["error","RangeError","oops",null,{"code":7,"detail":"x"}]`))
	if err != nil {
		t.Fatalf("decode error props: %v", err)
	}
	e, ok := decoded.(ErrorExpr)
	if !ok {
		t.Fatalf("expected ErrorExpr, got %T", decoded)
	}
	if e.Type != "RangeError" || e.Message != "oops" || e.Stack != "" {
		t.Errorf("unexpected error fields: %+v", e)
	}
	if len(e.Props) != 2 {
		t.Fatalf("expected 2 props, got %d (%+v)", len(e.Props), e.Props)
	}
	if lit, ok := e.Props["code"].(LiteralExpr); !ok || lit.Value != float64(7) {
		t.Errorf("props[code] = %#v, want literal 7", e.Props["code"])
	}
	if lit, ok := e.Props["detail"].(LiteralExpr); !ok || lit.Value != "x" {
		t.Errorf("props[detail] = %#v, want literal \"x\"", e.Props["detail"])
	}
}

// goldenMessages are the canonical wire forms for each message type.
var goldenMessages = []struct {
	name string
	msg  Message
	wire string
}{
	{"push", PushMsg{Expr: json.RawMessage(`["import",0,"foo",[]]`)}, `["push",["import",0,"foo",[]]]`},
	{"pull", PullMsg{ImportID: 3}, `["pull",3]`},
	{"resolve", ResolveMsg{ExportID: 2, Expr: json.RawMessage(`42`)}, `["resolve",2,42]`},
	{"reject", RejectMsg{ExportID: 2, Expr: json.RawMessage(`["error","Error","x"]`)}, `["reject",2,["error","Error","x"]]`},
	{"release", ReleaseMsg{ImportID: 5, RefCount: 1}, `["release",5,1]`},
	{"stream", StreamMsg{Expr: json.RawMessage(`["import",1,"write",[]]`)}, `["stream",["import",1,"write",[]]]`},
	{"pipe", PipeMsg{}, `["pipe"]`},
	{"abort", AbortMsg{Expr: json.RawMessage(`["error","Error","fatal"]`)}, `["abort",["error","Error","fatal"]]`},
}

func TestGoldenMessageMarshal(t *testing.T) {
	for _, tc := range goldenMessages {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MarshalMessage(tc.msg)
			if err != nil {
				t.Fatalf("MarshalMessage(%s): %v", tc.name, err)
			}
			if !jsonEq(t, got, []byte(tc.wire)) {
				t.Errorf("MarshalMessage(%s) = %s, want %s", tc.name, got, tc.wire)
			}
		})
	}
}

func TestGoldenMessageRoundTrip(t *testing.T) {
	for _, tc := range goldenMessages {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := UnmarshalMessage([]byte(tc.wire))
			if err != nil {
				t.Fatalf("UnmarshalMessage(%s): %v", tc.wire, err)
			}
			reencoded, err := MarshalMessage(decoded)
			if err != nil {
				t.Fatalf("MarshalMessage after decode (%s): %v", tc.name, err)
			}
			if !jsonEq(t, reencoded, []byte(tc.wire)) {
				t.Errorf("round-trip(%s) = %s, want %s", tc.name, reencoded, tc.wire)
			}
		})
	}
}
