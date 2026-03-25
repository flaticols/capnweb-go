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
		{name: "bytes", expr: BytesExpr{Data: []byte{0xDE, 0xAD}}, want: `["bytes","3q0="]`},
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
			expr: ImportExpr{ImportID: 0, Path: []string{"greet"}, Args: []Expr{LiteralExpr{Value: "hi"}}},
			want: `["import",0,"greet",["hi"]]`,
		},
		{
			name: "import with path no args",
			expr: ImportExpr{ImportID: 1, Path: []string{"name"}},
			want: `["import",1,"name"]`,
		},
		{
			name: "import call zero args",
			expr: ImportExpr{ImportID: 0, Path: []string{"ping"}, Args: []Expr{}},
			want: `["import",0,"ping",[]]`,
		},
		{
			name: "pipeline",
			expr: PipelineExpr{ImportID: 1, Path: []string{"getData"}, Args: []Expr{}},
			want: `["pipeline",1,"getData",[]]`,
		},

		// Streams
		{name: "writable", expr: WritableExpr{ExportID: -3}, want: `["writable",-3]`},
		{name: "readable", expr: ReadableExpr{ImportID: 5}, want: `["readable",5]`},

		// Remap
		{
			name: "remap",
			expr: RemapExpr{
				ImportID:     5,
				Path:         []string{},
				Captures:     []Expr{ImportExpr{ImportID: -3}},
				Instructions: []Expr{ImportExpr{ImportID: 0, Path: []string{"name"}}},
			},
			want: `["remap",5,[],[["import",-3]],[["import",0,"name"]]]`,
		},

		// Array
		{
			name: "array",
			expr: ArrayExpr{Elements: []Expr{LiteralExpr{Value: "a"}, LiteralExpr{Value: 1.0}}},
			want: `["a",1]`,
		},
		{name: "empty array", expr: ArrayExpr{Elements: []Expr{}}, want: `[]`},
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
	json.Unmarshal(arr[0], &tag)
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
	json.Unmarshal(arr[0], &tag)
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
