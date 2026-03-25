package capnweb

import (
	"bytes"
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
func DecodeExpr(data json.RawMessage) (Expr, error) {
	// Trim leading whitespace to reliably detect the value type.
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("capnweb: empty expression")
	}

	// Non-array values are literals.
	if trimmed[0] != '[' {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("capnweb: invalid literal: %w", err)
		}
		return LiteralExpr{Value: v}, nil
	}

	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("capnweb: invalid array: %w", err)
	}
	if len(raw) == 0 {
		return ArrayExpr{}, nil
	}

	// If first element isn't a string, it's a plain array.
	var tag string
	if err := json.Unmarshal(raw[0], &tag); err != nil {
		return decodeArrayElements(raw)
	}

	switch tag {
	case "undefined":
		return UndefinedExpr{}, nil
	case "inf":
		return InfExpr{}, nil
	case "-inf":
		return NegInfExpr{}, nil
	case "nan":
		return NaNExpr{}, nil

	case "bytes":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: bytes: missing data")
		}
		var s string
		if err := json.Unmarshal(raw[1], &s); err != nil {
			return nil, fmt.Errorf("capnweb: bytes: %w", err)
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("capnweb: bytes base64: %w", err)
		}
		return BytesExpr{Data: b}, nil

	case "bigint":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: bigint: missing value")
		}
		var s string
		if err := json.Unmarshal(raw[1], &s); err != nil {
			return nil, fmt.Errorf("capnweb: bigint: %w", err)
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("capnweb: invalid bigint %q", s)
		}
		return BigIntExpr{Value: n}, nil

	case "date":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: date: missing value")
		}
		var ms float64
		if err := json.Unmarshal(raw[1], &ms); err != nil {
			return nil, fmt.Errorf("capnweb: date: %w", err)
		}
		return DateExpr{Time: time.UnixMilli(int64(ms))}, nil

	case "error":
		if len(raw) < 3 {
			return nil, fmt.Errorf("capnweb: error: need type and message")
		}
		var typ, msg string
		if err := json.Unmarshal(raw[1], &typ); err != nil {
			return nil, fmt.Errorf("capnweb: error type: %w", err)
		}
		if err := json.Unmarshal(raw[2], &msg); err != nil {
			return nil, fmt.Errorf("capnweb: error message: %w", err)
		}
		var stack string
		if len(raw) > 3 {
			_ = json.Unmarshal(raw[3], &stack)
		}
		return ErrorExpr{Type: typ, Message: msg, Stack: stack}, nil

	case "headers":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: headers: missing pairs")
		}
		var pairs [][2]string
		if err := json.Unmarshal(raw[1], &pairs); err != nil {
			return nil, fmt.Errorf("capnweb: headers: %w", err)
		}
		h := make(http.Header, len(pairs))
		for _, p := range pairs {
			h.Add(p[0], p[1])
		}
		return HeadersExpr{Header: h}, nil

	case "request":
		if len(raw) < 3 {
			return nil, fmt.Errorf("capnweb: request: need url and init")
		}
		var url string
		if err := json.Unmarshal(raw[1], &url); err != nil {
			return nil, fmt.Errorf("capnweb: request url: %w", err)
		}
		var init struct {
			Method  string          `json:"method"`
			Headers [][2]string     `json:"headers"`
			Body    json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(raw[2], &init); err != nil {
			return nil, fmt.Errorf("capnweb: request init: %w", err)
		}
		req := RequestExpr{
			URL:    url,
			Method: init.Method,
		}
		if init.Headers != nil {
			req.Headers = make(http.Header, len(init.Headers))
			for _, p := range init.Headers {
				req.Headers.Add(p[0], p[1])
			}
		}
		if len(init.Body) > 0 && string(init.Body) != "null" {
			body, err := DecodeExpr(init.Body)
			if err != nil {
				return nil, fmt.Errorf("capnweb: request body: %w", err)
			}
			req.Body = body
		}
		return req, nil

	case "response":
		if len(raw) < 3 {
			return nil, fmt.Errorf("capnweb: response: need body and init")
		}
		var body Expr
		if string(raw[1]) != "null" {
			var err error
			body, err = DecodeExpr(raw[1])
			if err != nil {
				return nil, fmt.Errorf("capnweb: response body: %w", err)
			}
		}
		var init struct {
			Status     int         `json:"status"`
			StatusText string      `json:"statusText"`
			Headers    [][2]string `json:"headers"`
		}
		if err := json.Unmarshal(raw[2], &init); err != nil {
			return nil, fmt.Errorf("capnweb: response init: %w", err)
		}
		resp := ResponseExpr{
			Status:     init.Status,
			StatusText: init.StatusText,
			Body:       body,
		}
		if init.Headers != nil {
			resp.Headers = make(http.Header, len(init.Headers))
			for _, p := range init.Headers {
				resp.Headers.Add(p[0], p[1])
			}
		}
		return resp, nil

	case "import":
		return decodeRefAsImport(raw)
	case "pipeline":
		return decodeRefAsPipeline(raw)

	case "export":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: export: missing id")
		}
		var id int64
		if err := decodeInt64(raw[1], &id); err != nil {
			return nil, fmt.Errorf("capnweb: export id: %w", err)
		}
		return ExportExpr{ExportID: id}, nil

	case "promise":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: promise: missing id")
		}
		var id int64
		if err := decodeInt64(raw[1], &id); err != nil {
			return nil, fmt.Errorf("capnweb: promise id: %w", err)
		}
		return PromiseExpr{ExportID: id}, nil

	case "writable":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: writable: missing id")
		}
		var id int64
		if err := decodeInt64(raw[1], &id); err != nil {
			return nil, fmt.Errorf("capnweb: writable id: %w", err)
		}
		return WritableExpr{ExportID: id}, nil

	case "readable":
		if len(raw) < 2 {
			return nil, fmt.Errorf("capnweb: readable: missing id")
		}
		var id int64
		if err := decodeInt64(raw[1], &id); err != nil {
			return nil, fmt.Errorf("capnweb: readable id: %w", err)
		}
		return ReadableExpr{ImportID: id}, nil

	case "remap":
		if len(raw) < 5 {
			return nil, fmt.Errorf("capnweb: remap: need 5 elements, got %d", len(raw))
		}
		var id int64
		if err := decodeInt64(raw[1], &id); err != nil {
			return nil, fmt.Errorf("capnweb: remap id: %w", err)
		}
		var path []string
		if err := json.Unmarshal(raw[2], &path); err != nil {
			return nil, fmt.Errorf("capnweb: remap path: %w", err)
		}
		caps, err := decodeExprSlice(raw[3])
		if err != nil {
			return nil, fmt.Errorf("capnweb: remap captures: %w", err)
		}
		instrs, err := decodeExprSlice(raw[4])
		if err != nil {
			return nil, fmt.Errorf("capnweb: remap instructions: %w", err)
		}
		return RemapExpr{ImportID: id, Path: path, Captures: caps, Instructions: instrs}, nil

	default:
		// Unknown tag — treat as plain array.
		return decodeArrayElements(raw)
	}
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

// --- decoding helpers ---

func decodeInt64(data json.RawMessage, dst *int64) error {
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("expected number: %w", err)
	}
	v, err := n.Int64()
	if err != nil {
		return fmt.Errorf("expected integer: %w", err)
	}
	*dst = v
	return nil
}

func decodeArrayElements(raw []json.RawMessage) (Expr, error) {
	elems := make([]Expr, len(raw))
	for i, r := range raw {
		e, err := DecodeExpr(r)
		if err != nil {
			return nil, fmt.Errorf("capnweb: array[%d]: %w", i, err)
		}
		elems[i] = e
	}
	return ArrayExpr{Elements: elems}, nil
}

func decodeExprSlice(data json.RawMessage) ([]Expr, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]Expr, len(raw))
	for i, r := range raw {
		e, err := DecodeExpr(r)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out[i] = e
	}
	return out, nil
}

func decodeRefPath(raw []json.RawMessage) ([]string, error) {
	if len(raw) < 3 {
		return nil, nil
	}
	// Path can be a single string or array of strings.
	var s string
	if err := json.Unmarshal(raw[2], &s); err == nil {
		return []string{s}, nil
	}
	var path []string
	if err := json.Unmarshal(raw[2], &path); err != nil {
		return nil, fmt.Errorf("capnweb: ref path: %w", err)
	}
	return path, nil
}

func decodeRefArgs(raw []json.RawMessage) ([]Expr, error) {
	if len(raw) < 4 {
		return nil, nil
	}
	return decodeExprSlice(raw[3])
}

func decodeRefAsImport(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: import: missing id")
	}
	var id int64
	if err := decodeInt64(raw[1], &id); err != nil {
		return nil, fmt.Errorf("capnweb: import id: %w", err)
	}
	path, err := decodeRefPath(raw)
	if err != nil {
		return nil, err
	}
	args, err := decodeRefArgs(raw)
	if err != nil {
		return nil, fmt.Errorf("capnweb: import args: %w", err)
	}
	return ImportExpr{ImportID: id, Path: path, Args: args}, nil
}

func decodeRefAsPipeline(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: pipeline: missing id")
	}
	var id int64
	if err := decodeInt64(raw[1], &id); err != nil {
		return nil, fmt.Errorf("capnweb: pipeline id: %w", err)
	}
	path, err := decodeRefPath(raw)
	if err != nil {
		return nil, err
	}
	args, err := decodeRefArgs(raw)
	if err != nil {
		return nil, fmt.Errorf("capnweb: pipeline args: %w", err)
	}
	return PipelineExpr{ImportID: id, Path: path, Args: args}, nil
}
