package capnweb

import (
	"encoding/json"
	"testing"
)

func TestMessageRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		wire string
	}{
		{
			name: "push",
			msg:  PushMsg{Expr: json.RawMessage(`["import",0,"greet",["hello"]]`)},
			wire: `["push",["import",0,"greet",["hello"]]]`,
		},
		{
			name: "pull",
			msg:  PullMsg{ImportID: 1},
			wire: `["pull",1]`,
		},
		{
			name: "resolve",
			msg:  ResolveMsg{ExportID: 1, Expr: json.RawMessage(`"hello back"`)},
			wire: `["resolve",1,"hello back"]`,
		},
		{
			name: "reject",
			msg:  RejectMsg{ExportID: 1, Expr: json.RawMessage(`["error","Error","something failed"]`)},
			wire: `["reject",1,["error","Error","something failed"]]`,
		},
		{
			name: "release",
			msg:  ReleaseMsg{ImportID: 1, RefCount: 1},
			wire: `["release",1,1]`,
		},
		{
			name: "stream",
			msg:  StreamMsg{Expr: json.RawMessage(`["import",0,"write",[42]]`)},
			wire: `["stream",["import",0,"write",[42]]]`,
		},
		{
			name: "pipe",
			msg:  PipeMsg{},
			wire: `["pipe"]`,
		},
		{
			name: "abort",
			msg:  AbortMsg{Expr: json.RawMessage(`["error","Error","protocol violation"]`)},
			wire: `["abort",["error","Error","protocol violation"]]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/marshal", func(t *testing.T) {
			got, err := MarshalMessage(tt.msg)
			if err != nil {
				t.Fatalf("MarshalMessage: %v", err)
			}
			assertJSONEqual(t, tt.wire, string(got))
		})

		t.Run(tt.name+"/unmarshal", func(t *testing.T) {
			got, err := UnmarshalMessage([]byte(tt.wire))
			if err != nil {
				t.Fatalf("UnmarshalMessage(%s): %v", tt.wire, err)
			}
			// Re-marshal and compare to verify round-trip.
			b, err := MarshalMessage(got)
			if err != nil {
				t.Fatalf("re-MarshalMessage: %v", err)
			}
			assertJSONEqual(t, tt.wire, string(b))
		})
	}
}

func TestMessageRoundTripNegativeIDs(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		wire string
	}{
		{
			name: "resolve with negative export id",
			msg:  ResolveMsg{ExportID: -1, Expr: json.RawMessage(`"ok"`)},
			wire: `["resolve",-1,"ok"]`,
		},
		{
			name: "release with negative import id",
			msg:  ReleaseMsg{ImportID: -3, RefCount: 2},
			wire: `["release",-3,2]`,
		},
		{
			name: "pull with zero id",
			msg:  PullMsg{ImportID: 0},
			wire: `["pull",0]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MarshalMessage(tt.msg)
			if err != nil {
				t.Fatalf("MarshalMessage: %v", err)
			}
			assertJSONEqual(t, tt.wire, string(got))

			parsed, err := UnmarshalMessage([]byte(tt.wire))
			if err != nil {
				t.Fatalf("UnmarshalMessage: %v", err)
			}
			b, err := MarshalMessage(parsed)
			if err != nil {
				t.Fatalf("re-MarshalMessage: %v", err)
			}
			assertJSONEqual(t, tt.wire, string(b))
		})
	}
}

func TestUnmarshalMessageErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"not json", `{{{`},
		{"not array", `"hello"`},
		{"empty array", `[]`},
		{"type not string", `[123]`},
		{"unknown type", `["unknown"]`},
		{"push missing expr", `["push"]`},
		{"push too many", `["push",1,2]`},
		{"pull missing id", `["pull"]`},
		{"pull id not number", `["pull","abc"]`},
		{"resolve missing expr", `["resolve",1]`},
		{"resolve id not number", `["resolve","x","ok"]`},
		{"reject too few", `["reject",1]`},
		{"release too few", `["release",1]`},
		{"release refcount not number", `["release",1,"x"]`},
		{"stream missing expr", `["stream"]`},
		{"pipe extra elements", `["pipe",1]`},
		{"abort missing expr", `["abort"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UnmarshalMessage([]byte(tt.input))
			if err == nil {
				t.Fatalf("expected error for input %s", tt.input)
			}
		})
	}
}

func TestUnmarshalMessageTypeSwitching(t *testing.T) {
	// Verify we can type-switch on the result.
	msg, err := UnmarshalMessage([]byte(`["push",42]`))
	if err != nil {
		t.Fatal(err)
	}
	switch v := msg.(type) {
	case PushMsg:
		if string(v.Expr) != "42" {
			t.Fatalf("expected expr 42, got %s", v.Expr)
		}
	default:
		t.Fatalf("expected PushMsg, got %T", msg)
	}
}

// assertJSONEqual compares two JSON strings for semantic equality.
func assertJSONEqual(t *testing.T, want, got string) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("invalid expected JSON %q: %v", want, err)
	}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("invalid actual JSON %q: %v", got, err)
	}
	wb, _ := json.Marshal(w)
	gb, _ := json.Marshal(g)
	if string(wb) != string(gb) {
		t.Errorf("JSON mismatch:\n  want: %s\n  got:  %s", wb, gb)
	}
}
