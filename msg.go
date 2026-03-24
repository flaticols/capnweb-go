package capnweb

import (
	"encoding/json"
	"fmt"
)

// Message is a capnweb protocol message. Each concrete type corresponds to one
// of the 8 wire message types.
type Message interface {
	msg() // marker — prevents external implementations
}

// PushMsg requests the recipient to evaluate an expression.
// The sender implicitly assigns the next positive import ID to the result.
//
// Wire format: ["push", expression]
type PushMsg struct {
	Expr json.RawMessage
}

func (PushMsg) msg() {}

// PullMsg signals that the sender wants a resolve/reject for a promise import.
//
// Wire format: ["pull", importId]
type PullMsg struct {
	ImportID int64
}

func (PullMsg) msg() {}

// ResolveMsg delivers the resolution of a promise export.
//
// Wire format: ["resolve", exportId, expression]
type ResolveMsg struct {
	ExportID int64
	Expr     json.RawMessage
}

func (ResolveMsg) msg() {}

// RejectMsg delivers a rejection for a promise export. The expression must not
// contain stubs — it typically evaluates to an error.
//
// Wire format: ["reject", exportId, expression]
type RejectMsg struct {
	ExportID int64
	Expr     json.RawMessage
}

func (RejectMsg) msg() {}

// ReleaseMsg releases an import table entry.
//
// Wire format: ["release", importId, refcount]
type ReleaseMsg struct {
	ImportID int64
	RefCount int64
}

func (ReleaseMsg) msg() {}

// StreamMsg is like PushMsg but optimized for streaming: no pipelining on
// the result, auto-pulled, and implicitly released with refcount 1 on
// resolve/reject.
//
// Wire format: ["stream", expression]
type StreamMsg struct {
	Expr json.RawMessage
}

func (StreamMsg) msg() {}

// PipeMsg creates a bidirectional pipe on the remote end. The sender
// implicitly assigns the next positive import ID, usable as a WritableStream.
//
// Wire format: ["pipe"]
type PipeMsg struct{}

func (PipeMsg) msg() {}

// AbortMsg is a fatal error that terminates the session. No further messages
// are sent or received after this.
//
// Wire format: ["abort", expression]
type AbortMsg struct {
	Expr json.RawMessage
}

func (AbortMsg) msg() {}

// MarshalMessage encodes a Message to its JSON wire format.
func MarshalMessage(m Message) ([]byte, error) {
	var arr []any
	switch v := m.(type) {
	case PushMsg:
		arr = []any{"push", json.RawMessage(v.Expr)}
	case PullMsg:
		arr = []any{"pull", v.ImportID}
	case ResolveMsg:
		arr = []any{"resolve", v.ExportID, json.RawMessage(v.Expr)}
	case RejectMsg:
		arr = []any{"reject", v.ExportID, json.RawMessage(v.Expr)}
	case ReleaseMsg:
		arr = []any{"release", v.ImportID, v.RefCount}
	case StreamMsg:
		arr = []any{"stream", json.RawMessage(v.Expr)}
	case PipeMsg:
		arr = []any{"pipe"}
	case AbortMsg:
		arr = []any{"abort", json.RawMessage(v.Expr)}
	default:
		return nil, fmt.Errorf("capnweb: unknown message type %T", m)
	}
	return json.Marshal(arr)
}

// UnmarshalMessage decodes a JSON wire message into a Message.
func UnmarshalMessage(data []byte) (Message, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("capnweb: message must be a JSON array: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("capnweb: empty message array")
	}

	var tag string
	if err := json.Unmarshal(raw[0], &tag); err != nil {
		return nil, fmt.Errorf("capnweb: message type must be a string: %w", err)
	}

	switch tag {
	case "push":
		if len(raw) != 2 {
			return nil, fmt.Errorf("capnweb: push message requires 2 elements, got %d", len(raw))
		}
		return PushMsg{Expr: raw[1]}, nil

	case "pull":
		if len(raw) != 2 {
			return nil, fmt.Errorf("capnweb: pull message requires 2 elements, got %d", len(raw))
		}
		id, err := unmarshalInt64(raw[1], "pull importId")
		if err != nil {
			return nil, err
		}
		return PullMsg{ImportID: id}, nil

	case "resolve":
		if len(raw) != 3 {
			return nil, fmt.Errorf("capnweb: resolve message requires 3 elements, got %d", len(raw))
		}
		id, err := unmarshalInt64(raw[1], "resolve exportId")
		if err != nil {
			return nil, err
		}
		return ResolveMsg{ExportID: id, Expr: raw[2]}, nil

	case "reject":
		if len(raw) != 3 {
			return nil, fmt.Errorf("capnweb: reject message requires 3 elements, got %d", len(raw))
		}
		id, err := unmarshalInt64(raw[1], "reject exportId")
		if err != nil {
			return nil, err
		}
		return RejectMsg{ExportID: id, Expr: raw[2]}, nil

	case "release":
		if len(raw) != 3 {
			return nil, fmt.Errorf("capnweb: release message requires 3 elements, got %d", len(raw))
		}
		id, err := unmarshalInt64(raw[1], "release importId")
		if err != nil {
			return nil, err
		}
		rc, err := unmarshalInt64(raw[2], "release refcount")
		if err != nil {
			return nil, err
		}
		return ReleaseMsg{ImportID: id, RefCount: rc}, nil

	case "stream":
		if len(raw) != 2 {
			return nil, fmt.Errorf("capnweb: stream message requires 2 elements, got %d", len(raw))
		}
		return StreamMsg{Expr: raw[1]}, nil

	case "pipe":
		if len(raw) != 1 {
			return nil, fmt.Errorf("capnweb: pipe message requires 1 element, got %d", len(raw))
		}
		return PipeMsg{}, nil

	case "abort":
		if len(raw) != 2 {
			return nil, fmt.Errorf("capnweb: abort message requires 2 elements, got %d", len(raw))
		}
		return AbortMsg{Expr: raw[1]}, nil

	default:
		return nil, fmt.Errorf("capnweb: unknown message type %q", tag)
	}
}

func unmarshalInt64(data json.RawMessage, field string) (int64, error) {
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return 0, fmt.Errorf("capnweb: %s must be a number: %w", field, err)
	}
	v, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("capnweb: %s must be an integer: %w", field, err)
	}
	return v, nil
}
