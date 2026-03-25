package capnweb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// Expr represents a capnweb expression value. All values transmitted over the
// protocol are expressions — either literal JSON values or typed arrays like
// ["bytes", "..."], ["date", 123], ["import", 1, "method", [args]], etc.
//
// The array wrapping rule: non-array JSON values are interpreted literally.
// Arrays where element 0 is a recognized string tag are typed expressions.
// Actual plain arrays must be wrapped: [elem0, elem1] on the wire represents
// a two-element array, distinguished from expressions by the first element
// not being a known tag string (or by being an ArrayExpr during encoding).
type Expr interface {
	expr() // sealed marker
}

// --- Literal expressions ---

// LiteralExpr wraps a plain JSON value (string, number, bool, null, or object).
type LiteralExpr struct{ Value any }

func (LiteralExpr) expr() {}

// UndefinedExpr represents JavaScript undefined — ["undefined"].
type UndefinedExpr struct{}

func (UndefinedExpr) expr() {}

// InfExpr represents +Infinity — ["inf"].
type InfExpr struct{}

func (InfExpr) expr() {}

// NegInfExpr represents -Infinity — ["-inf"].
type NegInfExpr struct{}

func (NegInfExpr) expr() {}

// NaNExpr represents NaN — ["nan"].
type NaNExpr struct{}

func (NaNExpr) expr() {}

// --- Data type expressions ---

// BytesExpr represents a byte slice — ["bytes", base64].
type BytesExpr struct{ Data []byte }

func (BytesExpr) expr() {}

// BigIntExpr represents an arbitrary-precision integer — ["bigint", decimal].
type BigIntExpr struct{ Value *big.Int }

func (BigIntExpr) expr() {}

// DateExpr represents a timestamp — ["date", unixMillis].
type DateExpr struct{ Time time.Time }

func (DateExpr) expr() {}

// ErrorExpr represents a remote error — ["error", type, message, stack?].
type ErrorExpr struct {
	Type    string // "Error", "TypeError", "RangeError", etc.
	Message string
	Stack   string // optional
}

func (ErrorExpr) expr() {}

func (e ErrorExpr) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// HeadersExpr represents HTTP headers — ["headers", [[name, value], ...]].
type HeadersExpr struct{ Header http.Header }

func (HeadersExpr) expr() {}

// RequestExpr represents an HTTP request — ["request", url, init].
type RequestExpr struct {
	URL     string
	Method  string
	Headers http.Header
	Body    Expr // may be nil
}

func (RequestExpr) expr() {}

// ResponseExpr represents an HTTP response — ["response", body, init].
type ResponseExpr struct {
	Status     int
	StatusText string
	Headers    http.Header
	Body       Expr // may be nil
}

func (ResponseExpr) expr() {}

// --- Reference expressions (resolved by the session layer) ---

// ImportExpr references an import table entry — ["import", id, path?, args?].
// Evaluates to a stub.
type ImportExpr struct {
	ImportID int64
	Path     []string
	Args     []Expr // nil = no call; empty = call with zero args
}

func (ImportExpr) expr() {}

// PipelineExpr is like ImportExpr but evaluates to a promise —
// ["pipeline", id, path?, args?].
type PipelineExpr struct {
	ImportID int64
	Path     []string
	Args     []Expr
}

func (PipelineExpr) expr() {}

// ExportExpr exports a local object — ["export", id].
type ExportExpr struct{ ExportID int64 }

func (ExportExpr) expr() {}

// PromiseExpr exports a promise — ["promise", id].
type PromiseExpr struct{ ExportID int64 }

func (PromiseExpr) expr() {}

// --- Stream expressions ---

// WritableExpr references a writable stream — ["writable", exportId].
type WritableExpr struct{ ExportID int64 }

func (WritableExpr) expr() {}

// ReadableExpr references the readable end of a pipe — ["readable", importId].
type ReadableExpr struct{ ImportID int64 }

func (ReadableExpr) expr() {}

// --- Remap expression ---

// RemapExpr represents a server-side .map() — ["remap", importId, path, captures, instructions].
type RemapExpr struct {
	ImportID     int64
	Path         []string
	Captures     []Expr
	Instructions []Expr
}

func (RemapExpr) expr() {}

// --- Array wrapper ---

// ArrayExpr wraps a plain array of expressions. On the wire actual arrays are
// encoded as [elem0, elem1, ...] — the encoder handles distinguishing them
// from typed expression arrays.
type ArrayExpr struct{ Elements []Expr }

func (ArrayExpr) expr() {}

// EncodeExpr serializes an Expr to its JSON wire representation.
func EncodeExpr(e Expr) (json.RawMessage, error) {
	switch v := e.(type) {
	case LiteralExpr:
		return json.Marshal(v.Value)

	case UndefinedExpr:
		return json.Marshal([]string{"undefined"})
	case InfExpr:
		return json.Marshal([]string{"inf"})
	case NegInfExpr:
		return json.Marshal([]string{"-inf"})
	case NaNExpr:
		return json.Marshal([]string{"nan"})

	case BytesExpr:
		return json.Marshal([]any{"bytes", base64.StdEncoding.EncodeToString(v.Data)})
	case BigIntExpr:
		return json.Marshal([]any{"bigint", v.Value.String()})
	case DateExpr:
		return json.Marshal([]any{"date", v.Time.UnixMilli()})

	case ErrorExpr:
		if v.Stack != "" {
			return json.Marshal([]any{"error", v.Type, v.Message, v.Stack})
		}
		return json.Marshal([]any{"error", v.Type, v.Message})

	case HeadersExpr:
		return json.Marshal([]any{"headers", headerPairs(v.Header)})

	case RequestExpr:
		init := map[string]any{}
		if v.Method != "" {
			init["method"] = v.Method
		}
		if v.Headers != nil {
			init["headers"] = headerPairs(v.Headers)
		}
		if v.Body != nil {
			body, err := EncodeExpr(v.Body)
			if err != nil {
				return nil, fmt.Errorf("capnweb: request body: %w", err)
			}
			init["body"] = json.RawMessage(body)
		}
		return json.Marshal([]any{"request", v.URL, init})

	case ResponseExpr:
		init := map[string]any{}
		if v.Status != 0 {
			init["status"] = v.Status
		}
		if v.StatusText != "" {
			init["statusText"] = v.StatusText
		}
		if v.Headers != nil {
			init["headers"] = headerPairs(v.Headers)
		}
		var body json.RawMessage
		if v.Body != nil {
			var err error
			body, err = EncodeExpr(v.Body)
			if err != nil {
				return nil, fmt.Errorf("capnweb: response body: %w", err)
			}
		} else {
			body = json.RawMessage("null")
		}
		return json.Marshal([]any{"response", json.RawMessage(body), init})

	case ImportExpr:
		return encodeRefExpr("import", v.ImportID, v.Path, v.Args)
	case PipelineExpr:
		return encodeRefExpr("pipeline", v.ImportID, v.Path, v.Args)

	case ExportExpr:
		return json.Marshal([]any{"export", v.ExportID})
	case PromiseExpr:
		return json.Marshal([]any{"promise", v.ExportID})

	case WritableExpr:
		return json.Marshal([]any{"writable", v.ExportID})
	case ReadableExpr:
		return json.Marshal([]any{"readable", v.ImportID})

	case RemapExpr:
		caps, err := encodeExprSlice(v.Captures)
		if err != nil {
			return nil, fmt.Errorf("capnweb: remap captures: %w", err)
		}
		instrs, err := encodeExprSlice(v.Instructions)
		if err != nil {
			return nil, fmt.Errorf("capnweb: remap instructions: %w", err)
		}
		return json.Marshal([]any{"remap", v.ImportID, v.Path, caps, instrs})

	case ArrayExpr:
		encoded, err := encodeExprSlice(v.Elements)
		if err != nil {
			return nil, err
		}
		return json.Marshal(encoded)

	default:
		return nil, fmt.Errorf("capnweb: unknown expression type %T", e)
	}
}

// DecodeExpr deserializes a JSON wire value into an Expr.
func DecodeExpr(_ json.RawMessage) (Expr, error) {
	return nil, fmt.Errorf("capnweb: DecodeExpr not yet implemented")
}

// --- encoding helpers ---

func headerPairs(h http.Header) [][2]string {
	var pairs [][2]string
	for name, vals := range h {
		for _, v := range vals {
			pairs = append(pairs, [2]string{name, v})
		}
	}
	return pairs
}

func encodeRefExpr(tag string, id int64, path []string, args []Expr) (json.RawMessage, error) {
	elems := []any{tag, id}
	if len(path) > 0 || args != nil {
		if len(path) == 1 {
			elems = append(elems, path[0])
		} else {
			elems = append(elems, path)
		}
	}
	if args != nil {
		encoded, err := encodeExprSlice(args)
		if err != nil {
			return nil, fmt.Errorf("capnweb: %s args: %w", tag, err)
		}
		elems = append(elems, encoded)
	}
	return json.Marshal(elems)
}

func encodeExprSlice(exprs []Expr) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, len(exprs))
	for i, e := range exprs {
		b, err := EncodeExpr(e)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}
