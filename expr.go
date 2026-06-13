package capnweb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
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

// BlobExpr represents a Blob — ["blob", type, readableExpr]. The blob's bytes
// are streamed through a pipe (Body is normally a ReadableExpr), mirroring the
// JS reference, which cannot read a Blob synchronously and so always streams.
type BlobExpr struct {
	Type string // MIME type, e.g. "text/plain"
	Body Expr   // readable-stream expression carrying the bytes
}

func (BlobExpr) expr() {}

// BigIntExpr represents an arbitrary-precision integer — ["bigint", decimal].
type BigIntExpr struct{ Value *big.Int }

func (BigIntExpr) expr() {}

// DateExpr represents a timestamp — ["date", unixMillis], or a JS invalid Date
// — ["date", null] — when Invalid is set. Invalid is distinct from the zero
// Time so that a legitimate instant (e.g. 0001-01-01) still round-trips as a
// real timestamp rather than collapsing to null.
type DateExpr struct {
	Time    time.Time
	Invalid bool
}

func (DateExpr) expr() {}

// ErrorExpr represents a remote error — ["error", type, message, stack?, props?].
//
// Props carries the error's own enumerable properties plus the standard
// "cause" slot and (for AggregateError) "errors". When Props is non-empty the
// wire form is the 5-element ["error", type, message, stack-or-null, props];
// the stack slot is null when Stack is empty so props always lands at index 4.
// Errors with no extra properties keep the legacy 3- or 4-element form.
type ErrorExpr struct {
	Type    string // "Error", "TypeError", "RangeError", etc.
	Message string
	Stack   string          // optional
	Props   map[string]Expr // optional own/cause/errors properties
}

func (ErrorExpr) expr() {}

// Error implements the error interface, returning "Type: Message".
func (e ErrorExpr) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// NewTypeError creates an ErrorExpr with type "TypeError".
func NewTypeError(message string) *ErrorExpr {
	return &ErrorExpr{Type: "TypeError", Message: message}
}

// NewRangeError creates an ErrorExpr with type "RangeError".
func NewRangeError(message string) *ErrorExpr {
	return &ErrorExpr{Type: "RangeError", Message: message}
}

// NewReferenceError creates an ErrorExpr with type "ReferenceError".
func NewReferenceError(message string) *ErrorExpr {
	return &ErrorExpr{Type: "ReferenceError", Message: message}
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
	Path     []any  // property names (string) or array indices (number)
	Args     []Expr // nil = no call; empty = call with zero args
}

func (ImportExpr) expr() {}

// PipelineExpr is like ImportExpr but evaluates to a promise —
// ["pipeline", id, path?, args?].
type PipelineExpr struct {
	ImportID int64
	Path     []any
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
	Path         []any
	Captures     []Expr
	Instructions []Expr
}

func (RemapExpr) expr() {}

// --- Array wrapper ---

// ArrayExpr wraps a plain array of expressions. On the wire a literal array is
// "escaped" by wrapping it in a one-element outer array: [e0, e1, ...] is
// encoded as [[e0, e1, ...]], distinguishing it from a tagged expression.
type ArrayExpr struct{ Elements []Expr }

func (ArrayExpr) expr() {}

// ObjectExpr is a JSON object whose property values are themselves expressions
// — encoded/decoded as {"k": <expr>, ...}. Each value is recursively evaluated,
// so objects may carry nested arrays, dates, stubs, and other typed values.
type ObjectExpr struct{ Fields map[string]Expr }

func (ObjectExpr) expr() {}

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
		// Spec emits unpadded standard base64 (Uint8Array.toBase64{omitPadding}).
		return json.Marshal([]any{"bytes", base64.RawStdEncoding.EncodeToString(v.Data)})
	case BlobExpr:
		return encodeBlobExpr(v)
	case BigIntExpr:
		return json.Marshal([]any{"bigint", v.Value.String()})
	case DateExpr:
		if v.Invalid {
			// Mirror JS: an invalid/NaN Date serializes as ["date", null].
			return json.Marshal([]any{"date", nil})
		}
		return json.Marshal([]any{"date", v.Time.UnixMilli()})
	case ErrorExpr:
		return encodeErrorExpr(v)
	case HeadersExpr:
		return json.Marshal([]any{"headers", headerPairs(v.Header)})
	case RequestExpr:
		return encodeRequestExpr(v)
	case ResponseExpr:
		return encodeResponseExpr(v)
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
		return encodeRemapExpr(&v)
	case ArrayExpr:
		return encodeArrayExpr(v)
	case ObjectExpr:
		return encodeObjectExpr(v)
	default:
		return nil, fmt.Errorf("capnweb: unknown expression type %T", e)
	}
}

func encodeErrorExpr(v ErrorExpr) (json.RawMessage, error) {
	if len(v.Props) > 0 {
		props, err := encodeProps(v.Props)
		if err != nil {
			return nil, fmt.Errorf("capnweb: error props: %w", err)
		}
		// Normalize the stack slot to null so props is always at index 4.
		var stack any
		if v.Stack != "" {
			stack = v.Stack
		}
		return json.Marshal([]any{"error", v.Type, v.Message, stack, props})
	}
	if v.Stack != "" {
		return json.Marshal([]any{"error", v.Type, v.Message, v.Stack})
	}
	return json.Marshal([]any{"error", v.Type, v.Message})
}

func encodeProps(props map[string]Expr) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(props))
	for k, e := range props {
		b, err := EncodeExpr(e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = b
	}
	return out, nil
}

func encodeBlobExpr(v BlobExpr) (json.RawMessage, error) {
	if v.Body == nil {
		return nil, fmt.Errorf("capnweb: blob: missing body")
	}
	body, err := EncodeExpr(v.Body)
	if err != nil {
		return nil, fmt.Errorf("capnweb: blob body: %w", err)
	}
	return json.Marshal([]any{"blob", v.Type, body})
}

func encodeRequestExpr(v RequestExpr) (json.RawMessage, error) {
	init := map[string]any{}
	if v.Method != "" {
		init["method"] = v.Method
	}
	if len(v.Headers) > 0 {
		init["headers"] = headerPairs(v.Headers)
	}
	if v.Body != nil {
		body, err := EncodeExpr(v.Body)
		if err != nil {
			return nil, fmt.Errorf("capnweb: request body: %w", err)
		}
		init["body"] = body
		// A Request constructed with a body requires init.duplex; the reference
		// always sets "half".
		init["duplex"] = "half"
	}
	return json.Marshal([]any{"request", v.URL, init})
}

func encodeResponseExpr(v ResponseExpr) (json.RawMessage, error) {
	init := map[string]any{}
	if v.Status != 0 {
		init["status"] = v.Status
	}
	if v.StatusText != "" {
		init["statusText"] = v.StatusText
	}
	if len(v.Headers) > 0 {
		init["headers"] = headerPairs(v.Headers)
	}
	body, err := encodeOptionalBody(v.Body)
	if err != nil {
		return nil, err
	}
	return json.Marshal([]any{"response", body, init})
}

func encodeOptionalBody(e Expr) (json.RawMessage, error) {
	if e == nil {
		return json.RawMessage("null"), nil
	}
	return EncodeExpr(e)
}

func encodeRemapExpr(v *RemapExpr) (json.RawMessage, error) {
	caps, err := encodeExprSlice(v.Captures)
	if err != nil {
		return nil, fmt.Errorf("capnweb: remap captures: %w", err)
	}
	instrs, err := encodeExprSlice(v.Instructions)
	if err != nil {
		return nil, fmt.Errorf("capnweb: remap instructions: %w", err)
	}
	return json.Marshal([]any{"remap", v.ImportID, v.Path, caps, instrs})
}

func encodeArrayExpr(v ArrayExpr) (json.RawMessage, error) {
	encoded, err := encodeExprSlice(v.Elements)
	if err != nil {
		return nil, err
	}
	if encoded == nil {
		encoded = []json.RawMessage{}
	}
	// Escape literal arrays by wrapping in a one-element outer array: a plain
	// array [e0,e1,...] goes on the wire as [[e0,e1,...]] so it can't be
	// confused with a tagged expression.
	return json.Marshal([]any{encoded})
}

func encodeObjectExpr(v ObjectExpr) (json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(v.Fields))
	for k, e := range v.Fields {
		b, err := EncodeExpr(e)
		if err != nil {
			return nil, fmt.Errorf("capnweb: object field %q: %w", k, err)
		}
		out[k] = b
	}
	return json.Marshal(out)
}

// DecodeExpr deserializes a JSON wire value into an Expr.
func DecodeExpr(data json.RawMessage) (Expr, error) {
	// Trim leading whitespace to reliably detect the value type.
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("capnweb: empty expression")
	}

	// Objects: property values are themselves expressions — decode recursively.
	if trimmed[0] == '{' {
		return decodeObjectExpr(data)
	}

	// Non-array, non-object values are plain literals.
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

	// An array is an "escaped" literal array iff it has exactly one element
	// and that element is itself an array: [[...]] decodes to the inner array.
	if len(raw) == 1 && isJSONArray(raw[0]) {
		var inner []json.RawMessage
		if err := json.Unmarshal(raw[0], &inner); err != nil {
			return nil, fmt.Errorf("capnweb: invalid escaped array: %w", err)
		}
		return decodeArrayElements(inner)
	}

	// Otherwise the first element must be a recognized string type tag. A bare
	// or unknown-tagged array is not a valid expression (matches the reference,
	// which throws "unknown special value").
	if len(raw) == 0 {
		return nil, unknownSpecialValue(data)
	}
	var tag string
	if err := json.Unmarshal(raw[0], &tag); err != nil {
		return nil, unknownSpecialValue(data)
	}
	return decodeTaggedExpr(tag, raw)
}

func isJSONArray(data json.RawMessage) bool {
	t := bytes.TrimLeft(data, " \t\n\r")
	return len(t) > 0 && t[0] == '['
}

func unknownSpecialValue(data json.RawMessage) error {
	return fmt.Errorf("capnweb: unknown special value: %s", bytes.TrimSpace(data))
}

func decodeObjectExpr(data json.RawMessage) (Expr, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("capnweb: invalid object: %w", err)
	}
	fields := make(map[string]Expr, len(raw))
	for k, v := range raw {
		e, err := DecodeExpr(v)
		if err != nil {
			return nil, fmt.Errorf("capnweb: object field %q: %w", k, err)
		}
		fields[k] = e
	}
	return ObjectExpr{Fields: fields}, nil
}

// --- decode dispatch ---

func decodeTaggedExpr(tag string, raw []json.RawMessage) (Expr, error) {
	switch tag {
	case "undefined":
		if len(raw) != 1 {
			return nil, fmt.Errorf("capnweb: unknown special value: %s", joinRaw(raw))
		}
		return UndefinedExpr{}, nil
	case "inf":
		return InfExpr{}, nil
	case "-inf":
		return NegInfExpr{}, nil
	case "nan":
		return NaNExpr{}, nil
	case "bytes":
		return decodeBytesExpr(raw)
	case "blob":
		return decodeBlobExpr(raw)
	case "bigint":
		return decodeBigIntExpr(raw)
	case "date":
		return decodeDateExpr(raw)
	case "error":
		return decodeErrorExpr(raw)
	case "headers":
		return decodeHeadersExpr(raw)
	case "request":
		return decodeRequestExpr(raw)
	case "response":
		return decodeResponseExpr(raw)
	case "import":
		return decodeRefAsImport(raw)
	case "pipeline":
		return decodeRefAsPipeline(raw)
	case "export":
		return decodeIDExpr(raw, "export", func(id int64) Expr { return ExportExpr{ExportID: id} })
	case "promise":
		return decodeIDExpr(raw, "promise", func(id int64) Expr { return PromiseExpr{ExportID: id} })
	case "writable":
		return decodeIDExpr(raw, "writable", func(id int64) Expr { return WritableExpr{ExportID: id} })
	case "readable":
		return decodeIDExpr(raw, "readable", func(id int64) Expr { return ReadableExpr{ImportID: id} })
	case "remap":
		return decodeRemapExpr(raw)
	default:
		// An array whose first element is an unrecognized string tag is not a
		// valid expression (the reference throws "unknown special value").
		return nil, fmt.Errorf("capnweb: unknown special value: %s", joinRaw(raw))
	}
}

// joinRaw renders a decoded array back to a compact JSON-ish string for errors.
func joinRaw(raw []json.RawMessage) string {
	b, err := json.Marshal(raw)
	if err != nil {
		return "[?]"
	}
	return string(b)
}

func decodeIDExpr(raw []json.RawMessage, tag string, make_ func(int64) Expr) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: %s: missing id", tag)
	}
	var id int64
	if err := decodeInt64(raw[1], &id); err != nil {
		return nil, fmt.Errorf("capnweb: %s id: %w", tag, err)
	}
	return make_(id), nil
}

func decodeBytesExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: bytes: missing data")
	}
	var s string
	if err := json.Unmarshal(raw[1], &s); err != nil {
		return nil, fmt.Errorf("capnweb: bytes: %w", err)
	}
	// Accept both unpadded (spec/JS) and padded base64 by stripping any padding
	// and decoding with the raw (unpadded) decoder.
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("capnweb: bytes base64: %w", err)
	}
	return BytesExpr{Data: b}, nil
}

func decodeBlobExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 3 {
		return nil, fmt.Errorf("capnweb: blob: need type and body")
	}
	var typ string
	if err := json.Unmarshal(raw[1], &typ); err != nil {
		return nil, fmt.Errorf("capnweb: blob type: %w", err)
	}
	body, err := DecodeExpr(raw[2])
	if err != nil {
		return nil, fmt.Errorf("capnweb: blob body: %w", err)
	}
	return BlobExpr{Type: typ, Body: body}, nil
}

func decodeBigIntExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: bigint: missing value")
	}
	var s string
	if err := json.Unmarshal(raw[1], &s); err != nil {
		return nil, fmt.Errorf("capnweb: bigint: %w", err)
	}
	n, ok := parseJSBigInt(s)
	if !ok {
		return nil, fmt.Errorf("capnweb: invalid bigint %q", s)
	}
	return BigIntExpr{Value: n}, nil
}

// parseJSBigInt parses a decimal string the way JS BigInt() does: surrounding
// whitespace is ignored, "" is 0, 0x/0o/0b prefixes select hex/octal/binary,
// and a leading "0" is still decimal (unlike C/Go base-0). A sign is allowed
// only on decimal; a sign combined with a radix prefix, or a prefix with no
// digits, is invalid (BigInt throws).
func parseJSBigInt(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return big.NewInt(0), true
	}
	signed := s[0] == '+' || s[0] == '-'
	neg := s[0] == '-'
	body := s
	if signed {
		body = s[1:]
	}
	base := 10
	if len(body) >= 2 && body[0] == '0' {
		switch body[1] {
		case 'x', 'X':
			base, body = 16, body[2:]
		case 'o', 'O':
			base, body = 8, body[2:]
		case 'b', 'B':
			base, body = 2, body[2:]
		}
	}
	if base != 10 && signed {
		return nil, false // JS rejects e.g. "-0x10"
	}
	if body == "" {
		return nil, false
	}
	n, ok := new(big.Int).SetString(body, base)
	if !ok {
		return nil, false
	}
	if neg {
		n.Neg(n)
	}
	return n, true
}

func decodeDateExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: date: missing value")
	}
	if string(bytes.TrimSpace(raw[1])) == "null" {
		// JS invalid/NaN Date.
		return DateExpr{Invalid: true}, nil
	}
	var ms float64
	if err := json.Unmarshal(raw[1], &ms); err != nil {
		return nil, fmt.Errorf("capnweb: date: %w", err)
	}
	return DateExpr{Time: time.UnixMilli(int64(ms))}, nil
}

func decodeErrorExpr(raw []json.RawMessage) (Expr, error) {
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
	if len(raw) > 3 && string(bytes.TrimSpace(raw[3])) != "null" {
		_ = json.Unmarshal(raw[3], &stack)
	}
	props, err := decodeProps(raw)
	if err != nil {
		return nil, err
	}
	return ErrorExpr{Type: typ, Message: msg, Stack: stack, Props: props}, nil
}

func decodeProps(raw []json.RawMessage) (map[string]Expr, error) {
	if len(raw) < 5 || string(bytes.TrimSpace(raw[4])) == "null" {
		return nil, nil
	}
	var rawProps map[string]json.RawMessage
	if json.Unmarshal(raw[4], &rawProps) != nil {
		// Malformed props bag: ignore it; the error itself still propagates.
		return nil, nil //nolint:nilerr // intentionally drop unparseable props
	}
	props := make(map[string]Expr, len(rawProps))
	for k, v := range rawProps {
		e, err := DecodeExpr(v)
		if err != nil {
			return nil, fmt.Errorf("capnweb: error prop %q: %w", k, err)
		}
		props[k] = e
	}
	return props, nil
}

func decodeHeadersExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("capnweb: headers: missing pairs")
	}
	var pairs [][2]string
	if err := json.Unmarshal(raw[1], &pairs); err != nil {
		return nil, fmt.Errorf("capnweb: headers: %w", err)
	}
	return HeadersExpr{Header: pairsToHeader(pairs)}, nil
}

func pairsToHeader(pairs [][2]string) http.Header {
	h := make(http.Header, len(pairs))
	for _, p := range pairs {
		h.Add(p[0], p[1])
	}
	return h
}

func decodeRequestExpr(raw []json.RawMessage) (Expr, error) {
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
	req := RequestExpr{URL: url, Method: init.Method}
	if init.Headers != nil {
		req.Headers = pairsToHeader(init.Headers)
	}
	if len(init.Body) > 0 && string(init.Body) != "null" {
		body, err := DecodeExpr(init.Body)
		if err != nil {
			return nil, fmt.Errorf("capnweb: request body: %w", err)
		}
		req.Body = body
	}
	return req, nil
}

func decodeResponseExpr(raw []json.RawMessage) (Expr, error) {
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
	resp := ResponseExpr{Status: init.Status, StatusText: init.StatusText, Body: body}
	if init.Headers != nil {
		resp.Headers = pairsToHeader(init.Headers)
	}
	return resp, nil
}

func decodeRemapExpr(raw []json.RawMessage) (Expr, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("capnweb: remap: need 5 elements, got %d", len(raw))
	}
	var id int64
	if err := decodeInt64(raw[1], &id); err != nil {
		return nil, fmt.Errorf("capnweb: remap id: %w", err)
	}
	path, err := decodePathElems(raw[2])
	if err != nil {
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
}

// --- encoding helpers ---

func headerPairs(h http.Header) [][2]string {
	// Non-nil so an empty header encodes as [] (not null), matching the
	// reference's array-of-pairs form.
	pairs := make([][2]string, 0, len(h))
	for name, vals := range h {
		// The Fetch Headers iterator combines duplicate values for a field into
		// one ", "-joined entry; emit one pair per name to match.
		pairs = append(pairs, [2]string{name, strings.Join(vals, ", ")})
	}
	// Sort by name so the wire output is deterministic.
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	return pairs
}

func encodeRefExpr(tag string, id int64, path []any, args []Expr) (json.RawMessage, error) {
	elems := []any{tag, id}
	if len(path) > 0 || args != nil {
		// The property path is always an array of names (strings) or indices
		// (numbers), even for a single element; the reference rejects a
		// bare-string path. Use a non-nil slice so an empty path with args
		// encodes as [] rather than null.
		p := path
		if p == nil {
			p = []any{}
		}
		elems = append(elems, p)
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

func decodeRefPath(raw []json.RawMessage) ([]any, error) {
	if len(raw) < 3 {
		return nil, nil
	}
	// Tolerate the legacy bare-string path form.
	var s string
	if err := json.Unmarshal(raw[2], &s); err == nil {
		return []any{s}, nil
	}
	return decodePathElems(raw[2])
}

// decodePathElems decodes a property path: an array whose elements are strings
// (property names) or numbers (array indices), per the spec's PropertyPath.
func decodePathElems(data json.RawMessage) ([]any, error) {
	var path []any
	if err := json.Unmarshal(data, &path); err != nil {
		return nil, fmt.Errorf("capnweb: ref path: %w", err)
	}
	for i, p := range path {
		switch p.(type) {
		case string, float64:
		default:
			return nil, fmt.Errorf("capnweb: ref path[%d]: expected string or number, got %T", i, p)
		}
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
	id, path, args, err := decodeRefFields("import", raw)
	if err != nil {
		return nil, err
	}
	return ImportExpr{ImportID: id, Path: path, Args: args}, nil
}

func decodeRefAsPipeline(raw []json.RawMessage) (Expr, error) {
	id, path, args, err := decodeRefFields("pipeline", raw)
	if err != nil {
		return nil, err
	}
	return PipelineExpr{ImportID: id, Path: path, Args: args}, nil
}

func decodeRefFields(tag string, raw []json.RawMessage) (id int64, path []any, args []Expr, err error) {
	if len(raw) < 2 {
		err = fmt.Errorf("capnweb: %s: missing id", tag)
		return
	}
	if err = decodeInt64(raw[1], &id); err != nil {
		err = fmt.Errorf("capnweb: %s id: %w", tag, err)
		return
	}
	if path, err = decodeRefPath(raw); err != nil {
		return
	}
	if args, err = decodeRefArgs(raw); err != nil {
		err = fmt.Errorf("capnweb: %s args: %w", tag, err)
	}
	return
}
