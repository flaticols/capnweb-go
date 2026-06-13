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
	{"import_call", ImportExpr{ImportID: 1, Path: []any{"add"}, Args: []Expr{LiteralExpr{Value: float64(1)}, LiteralExpr{Value: float64(2)}}}, `["import",1,["add"],[1,2]]`},
	{"pipeline_call", PipelineExpr{ImportID: 2, Path: []any{"foo"}, Args: []Expr{}}, `["pipeline",2,["foo"],[]]`},

	// HTTP types.
	{"headers", HeadersExpr{Header: http.Header{"X-A": {"1"}}}, `["headers",[["X-A","1"]]]`},
	// Duplicate values for a field are comma-combined into one pair.
	{"headers_multi", HeadersExpr{Header: http.Header{"X-A": {"1", "2"}}}, `["headers",[["X-A","1, 2"]]]`},
	// An empty header is an empty array, not null.
	{"headers_empty", HeadersExpr{Header: http.Header{}}, `["headers",[]]`},

	// Remap.
	{
		"remap",
		RemapExpr{ImportID: 1, Path: []any{}, Captures: []Expr{}, Instructions: []Expr{ImportExpr{ImportID: 0, Path: []any{"x"}, Args: []Expr{}}}},
		`["remap",1,[],[],[["import",0,["x"],[]]]]`,
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

// TestEmptyHeadersOmittedInInit verifies request/response init omits the
// "headers" key entirely when the header set is empty (rather than emitting
// "headers":null).
func TestEmptyHeadersOmittedInInit(t *testing.T) {
	got, err := EncodeExpr(RequestExpr{URL: "http://x", Method: "GET", Headers: http.Header{}})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(got, &arr); err != nil || len(arr) != 3 {
		t.Fatalf("bad request wire %s: %v", got, err)
	}
	var init map[string]json.RawMessage
	if err := json.Unmarshal(arr[2], &init); err != nil {
		t.Fatalf("bad init: %v", err)
	}
	if _, present := init["headers"]; present {
		t.Errorf("init should omit empty headers; got %s", arr[2])
	}
}

// TestRefPathAlwaysArray verifies a single-element property path encodes as an
// array (the reference rejects a bare-string path), while decode still tolerates
// the legacy bare-string form.
func TestRefPathAlwaysArray(t *testing.T) {
	got, err := EncodeExpr(ImportExpr{ImportID: 1, Path: []any{"m"}, Args: []Expr{}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !jsonEq(t, got, []byte(`["import",1,["m"],[]]`)) {
		t.Errorf("single-element path = %s; want array form", got)
	}

	// Decode tolerates both the array form and the legacy bare string.
	for _, wire := range []string{`["import",1,["m"]]`, `["import",1,"m"]`} {
		decoded, err := DecodeExpr([]byte(wire))
		if err != nil {
			t.Fatalf("decode %s: %v", wire, err)
		}
		imp, ok := decoded.(ImportExpr)
		if !ok || len(imp.Path) != 1 || imp.Path[0] != "m" {
			t.Errorf("decode %s = %#v; want path [m]", wire, decoded)
		}
	}
}

// TestNumericPropertyPath verifies a property path may carry numeric indices
// (PropertyPath = (string|number)[]) without the decoder rejecting them.
func TestNumericPropertyPath(t *testing.T) {
	decoded, err := DecodeExpr([]byte(`["pipeline",0,[0,"name"]]`))
	if err != nil {
		t.Fatalf("decode numeric path: %v", err)
	}
	p, ok := decoded.(PipelineExpr)
	if !ok {
		t.Fatalf("expected PipelineExpr, got %T", decoded)
	}
	if len(p.Path) != 2 || p.Path[0] != float64(0) || p.Path[1] != "name" {
		t.Errorf("path = %#v; want [0 name]", p.Path)
	}
	// accessPath indexes into arrays by number (float64 from JSON) and by a
	// numeric string (JS property keys are strings), and into objects by name.
	if got := accessPath([]any{map[string]any{"name": "x"}}, []any{float64(0), "name"}); got != "x" {
		t.Errorf("accessPath numeric = %v; want x", got)
	}
	if got := accessPath([]any{float64(10), float64(20)}, []any{"1"}); got != float64(20) {
		t.Errorf("accessPath string-index = %v; want 20", got)
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
