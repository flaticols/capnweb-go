package capnweb

import (
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
	"time"
)

func TestEncodeExpr(t *testing.T) {
	tests := []struct {
		name string
		expr Expr
		want string
	}{
		// Literals
		{name: "string", expr: LiteralExpr{Value: "hello"}, want: `"hello"`},
		{name: "number", expr: LiteralExpr{Value: 42.0}, want: `42`},
		{name: "bool", expr: LiteralExpr{Value: true}, want: `true`},
		{name: "null", expr: LiteralExpr{Value: nil}, want: `null`},
		{name: "object", expr: LiteralExpr{Value: map[string]any{"a": 1.0}}, want: `{"a":1}`},

		// Special values
		{name: "undefined", expr: UndefinedExpr{}, want: `["undefined"]`},
		{name: "inf", expr: InfExpr{}, want: `["inf"]`},
		{name: "-inf", expr: NegInfExpr{}, want: `["-inf"]`},
		{name: "nan", expr: NaNExpr{}, want: `["nan"]`},

		// Data types
		{name: "bytes", expr: BytesExpr{Data: []byte{0xDE, 0xAD}}, want: `["bytes","3q0"]`},
		{name: "bytes empty", expr: BytesExpr{Data: []byte{}}, want: `["bytes",""]`},
		{name: "bigint", expr: BigIntExpr{Value: big.NewInt(999999999999)}, want: `["bigint","999999999999"]`},
		{name: "bigint negative", expr: BigIntExpr{Value: big.NewInt(-42)}, want: `["bigint","-42"]`},
		{name: "date", expr: DateExpr{Time: time.UnixMilli(1700000000000)}, want: `["date",1700000000000]`},

		// Error
		{name: "error", expr: ErrorExpr{Type: "TypeError", Message: "bad arg"}, want: `["error","TypeError","bad arg"]`},
		{name: "error with stack", expr: ErrorExpr{Type: "Error", Message: "oops", Stack: "at foo:1"}, want: `["error","Error","oops","at foo:1"]`},

		// Headers
		{
			name: "headers",
			expr: HeadersExpr{Header: http.Header{"Content-Type": {"application/json"}}},
			want: `["headers",[["Content-Type","application/json"]]]`,
		},

		// References
		{name: "export", expr: ExportExpr{ExportID: -1}, want: `["export",-1]`},
		{name: "promise", expr: PromiseExpr{ExportID: -2}, want: `["promise",-2]`},
		{name: "import id only", expr: ImportExpr{ImportID: 0}, want: `["import",0]`},
		{
			name: "import with method",
			expr: ImportExpr{ImportID: 0, Path: []any{"greet"}, Args: []Expr{LiteralExpr{Value: "hi"}}},
			want: `["import",0,["greet"],["hi"]]`,
		},
		{
			name: "import with path no args",
			expr: ImportExpr{ImportID: 1, Path: []any{"name"}},
			want: `["import",1,["name"]]`,
		},
		{
			name: "import call zero args",
			expr: ImportExpr{ImportID: 0, Path: []any{"ping"}, Args: []Expr{}},
			want: `["import",0,["ping"],[]]`,
		},
		{
			name: "pipeline",
			expr: PipelineExpr{ImportID: 1, Path: []any{"getData"}, Args: []Expr{}},
			want: `["pipeline",1,["getData"],[]]`,
		},

		// Streams
		{name: "writable", expr: WritableExpr{ExportID: -3}, want: `["writable",-3]`},
		{name: "readable", expr: ReadableExpr{ImportID: 5}, want: `["readable",5]`},

		// Remap
		{
			name: "remap",
			expr: RemapExpr{
				ImportID:     5,
				Path:         []any{},
				Captures:     []Expr{ImportExpr{ImportID: -3}},
				Instructions: []Expr{ImportExpr{ImportID: 0, Path: []any{"name"}}},
			},
			want: `["remap",5,[],[["import",-3]],[["import",0,["name"]]]]`,
		},

		// Array
		{
			name: "array",
			expr: ArrayExpr{Elements: []Expr{LiteralExpr{Value: "a"}, LiteralExpr{Value: 1.0}}},
			want: `[["a",1]]`,
		},
		{name: "empty array", expr: ArrayExpr{Elements: []Expr{}}, want: `[[]]`},
		{
			name: "object",
			expr: ObjectExpr{Fields: map[string]Expr{"k": ArrayExpr{Elements: []Expr{LiteralExpr{Value: 1.0}}}}},
			want: `{"k":[[1]]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeExpr(tt.expr)
			if err != nil {
				t.Fatalf("EncodeExpr: %v", err)
			}
			assertJSONEq(t, tt.want, string(got))
		})
	}
}

func TestEncodeExprRequest(t *testing.T) {
	got, err := EncodeExpr(RequestExpr{
		URL:     "https://example.com",
		Method:  "POST",
		Headers: http.Header{"X-Key": {"val"}},
		Body:    BytesExpr{Data: []byte{1, 2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal to verify structure since map key order isn't guaranteed.
	var arr []json.RawMessage
	if err := json.Unmarshal(got, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr))
	}

	var tag string
	_ = json.Unmarshal(arr[0], &tag)
	if tag != "request" {
		t.Fatalf("tag = %q, want request", tag)
	}

	var url string
	json.Unmarshal(arr[1], &url)
	if url != "https://example.com" {
		t.Fatalf("url = %q", url)
	}

	var init map[string]json.RawMessage
	json.Unmarshal(arr[2], &init)
	if string(init["method"]) != `"POST"` {
		t.Fatalf("method = %s", init["method"])
	}
}

func TestEncodeExprResponse(t *testing.T) {
	got, err := EncodeExpr(ResponseExpr{
		Status:     200,
		StatusText: "OK",
		Headers:    http.Header{"Content-Type": {"text/plain"}},
		Body:       LiteralExpr{Value: "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(got, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr))
	}

	var tag string
	_ = json.Unmarshal(arr[0], &tag)
	if tag != "response" {
		t.Fatalf("tag = %q", tag)
	}
}

func TestEncodeExprResponseNilBody(t *testing.T) {
	got, err := EncodeExpr(ResponseExpr{Status: 204})
	if err != nil {
		t.Fatal(err)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(got, &arr); err != nil {
		t.Fatal(err)
	}
	if string(arr[1]) != "null" {
		t.Fatalf("expected null body, got %s", arr[1])
	}
}

func TestDecodeExpr(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want Expr
	}{
		// Literals
		{name: "string", wire: `"hello"`, want: LiteralExpr{Value: "hello"}},
		{name: "number", wire: `42`, want: LiteralExpr{Value: 42.0}},
		{name: "bool", wire: `true`, want: LiteralExpr{Value: true}},
		{name: "null", wire: `null`, want: LiteralExpr{Value: nil}},

		// Special values
		{name: "undefined", wire: `["undefined"]`, want: UndefinedExpr{}},
		{name: "inf", wire: `["inf"]`, want: InfExpr{}},
		{name: "-inf", wire: `["-inf"]`, want: NegInfExpr{}},
		{name: "nan", wire: `["nan"]`, want: NaNExpr{}},

		// Data types
		{name: "bytes", wire: `["bytes","3q0="]`, want: BytesExpr{Data: []byte{0xDE, 0xAD}}},
		{name: "bytes unpadded", wire: `["bytes","3q0"]`, want: BytesExpr{Data: []byte{0xDE, 0xAD}}},
		{name: "bytes unpadded single", wire: `["bytes","AQ"]`, want: BytesExpr{Data: []byte{1}}},
		{name: "bigint", wire: `["bigint","999999999999"]`, want: BigIntExpr{Value: big.NewInt(999999999999)}},
		{name: "date", wire: `["date",1700000000000]`, want: DateExpr{Time: time.UnixMilli(1700000000000)}},
		{name: "error", wire: `["error","TypeError","bad"]`, want: ErrorExpr{Type: "TypeError", Message: "bad"}},
		{name: "error+stack", wire: `["error","Error","x","at y:1"]`, want: ErrorExpr{Type: "Error", Message: "x", Stack: "at y:1"}},

		// References
		{name: "export", wire: `["export",-1]`, want: ExportExpr{ExportID: -1}},
		{name: "promise", wire: `["promise",-2]`, want: PromiseExpr{ExportID: -2}},
		{name: "import id only", wire: `["import",5]`, want: ImportExpr{ImportID: 5}},
		{name: "import+method+args", wire: `["import",0,["greet"],["hi"]]`, want: ImportExpr{ImportID: 0, Path: []any{"greet"}, Args: []Expr{LiteralExpr{Value: "hi"}}}},
		{name: "pipeline", wire: `["pipeline",1,["getData"],[]]`, want: PipelineExpr{ImportID: 1, Path: []any{"getData"}, Args: []Expr{}}},

		// Streams
		{name: "writable", wire: `["writable",-3]`, want: WritableExpr{ExportID: -3}},
		{name: "readable", wire: `["readable",5]`, want: ReadableExpr{ImportID: 5}},

		// Escaped arrays
		{name: "empty array", wire: `[[]]`, want: ArrayExpr{}},
		{name: "escaped array", wire: `[["a",1]]`, want: ArrayExpr{Elements: []Expr{LiteralExpr{Value: "a"}, LiteralExpr{Value: 1.0}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeExpr(json.RawMessage(tt.wire))
			if err != nil {
				t.Fatalf("DecodeExpr(%s): %v", tt.wire, err)
			}
			// Round-trip: encode back and compare JSON.
			encoded, err := EncodeExpr(got)
			if err != nil {
				t.Fatalf("EncodeExpr after decode: %v", err)
			}
			wantEncoded, err := EncodeExpr(tt.want)
			if err != nil {
				t.Fatalf("EncodeExpr(want): %v", err)
			}
			assertJSONEq(t, string(wantEncoded), string(encoded))
		})
	}
}

func TestDecodeExprRoundTrip(t *testing.T) {
	// Encode → Decode → Encode and verify JSON stability.
	exprs := []struct {
		name string
		expr Expr
	}{
		{"literal string", LiteralExpr{Value: "test"}},
		{"literal number", LiteralExpr{Value: 3.14}},
		{"undefined", UndefinedExpr{}},
		{"bytes", BytesExpr{Data: []byte{1, 2, 3}}},
		{"bigint", BigIntExpr{Value: big.NewInt(-12345)}},
		{"date", DateExpr{Time: time.UnixMilli(1700000000000)}},
		{"error", ErrorExpr{Type: "RangeError", Message: "out of bounds"}},
		{"export", ExportExpr{ExportID: -5}},
		{"import+call", ImportExpr{ImportID: 0, Path: []any{"foo"}, Args: []Expr{LiteralExpr{Value: 42.0}}}},
		{"pipeline", PipelineExpr{ImportID: 1, Path: []any{"bar"}, Args: []Expr{}}},
		{"writable", WritableExpr{ExportID: -1}},
		{"readable", ReadableExpr{ImportID: 3}},
		{"array", ArrayExpr{Elements: []Expr{LiteralExpr{Value: "a"}, LiteralExpr{Value: 1.0}}}},
	}

	for _, tt := range exprs {
		t.Run(tt.name, func(t *testing.T) {
			j1, err := EncodeExpr(tt.expr)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			decoded, err := DecodeExpr(j1)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			j2, err := EncodeExpr(decoded)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			assertJSONEq(t, string(j1), string(j2))
		})
	}
}

func TestDecodeExprErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"bytes missing data", `["bytes"]`},
		{"bytes bad base64", `["bytes","!!!"]`},
		{"bigint missing", `["bigint"]`},
		{"bigint bad", `["bigint","abc"]`},
		{"date missing", `["date"]`},
		{"error too few", `["error","TypeError"]`},
		{"headers missing", `["headers"]`},
		{"export missing id", `["export"]`},
		{"promise missing id", `["promise"]`},
		{"import missing id", `["import"]`},
		{"writable missing id", `["writable"]`},
		{"readable missing id", `["readable"]`},
		{"remap too few", `["remap",1]`},
		{"request too few", `["request"]`},
		{"response too few", `["response"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeExpr(json.RawMessage(tt.input))
			if err == nil {
				t.Fatalf("expected error for %s", tt.input)
			}
		})
	}
}

func FuzzDecodeExpr(f *testing.F) {
	// Seed corpus: valid expressions of every kind.
	seeds := []string{
		`"hello"`,
		`42`,
		`true`,
		`null`,
		`{"a":1}`,
		`[]`,
		`["undefined"]`,
		`["inf"]`,
		`["-inf"]`,
		`["nan"]`,
		`["bytes","AQID"]`,
		`["bigint","12345"]`,
		`["date",1700000000000]`,
		`["error","Error","boom"]`,
		`["error","TypeError","bad","at x:1"]`,
		`["headers",[["Content-Type","text/plain"]]]`,
		`["export",-1]`,
		`["promise",-2]`,
		`["import",0]`,
		`["import",0,["greet"],["hi"]]`,
		`["pipeline",1,["getData"],[]]`,
		`["writable",-3]`,
		`["readable",5]`,
		`["remap",5,[],[["import",-3]],[["import",0,["name"]]]]`,
		`["request","https://example.com",{"method":"GET"}]`,
		`["response","ok",{"status":200}]`,
		`[1,2,3]`,
		// Malformed inputs.
		``,
		`{`,
		`[`,
		`["bytes"]`,
		`["bigint"]`,
		`["unknown_tag",1,2,3]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		expr, err := DecodeExpr(json.RawMessage(data))
		if err != nil {
			return // invalid input is fine, just must not panic
		}

		// If decode succeeded, encode must also succeed.
		encoded, err := EncodeExpr(expr)
		if err != nil {
			t.Fatalf("EncodeExpr failed after successful decode: %v\ninput: %s", err, data)
		}

		// Re-decode the encoded output must also succeed.
		_, err = DecodeExpr(encoded)
		if err != nil {
			t.Fatalf("DecodeExpr failed on re-encoded output: %v\noriginal: %s\nencoded: %s", err, data, encoded)
		}
	})
}

// assertJSONEq compares two JSON strings semantically.
func assertJSONEq(t *testing.T, want, got string) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad expected JSON %q: %v", want, err)
	}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("bad actual JSON %q: %v", got, err)
	}
	wb, _ := json.Marshal(w)
	gb, _ := json.Marshal(g)
	if string(wb) != string(gb) {
		t.Errorf("JSON mismatch:\n  want: %s\n  got:  %s", wb, gb)
	}
}
