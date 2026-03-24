package capnweb

import (
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

// --- Encode / Decode stubs (to be implemented) ---

// EncodeExpr serializes an Expr to its JSON wire representation.
func EncodeExpr(_ Expr) (json.RawMessage, error) {
	return nil, fmt.Errorf("capnweb: EncodeExpr not yet implemented")
}

// DecodeExpr deserializes a JSON wire value into an Expr.
func DecodeExpr(_ json.RawMessage) (Expr, error) {
	return nil, fmt.Errorf("capnweb: DecodeExpr not yet implemented")
}
