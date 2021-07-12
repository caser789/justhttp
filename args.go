package fasthttp

import (
	"bytes"
	"errors"
)

// Args represents query arguments
//
// It is forbidden copying Args instances. Create new instances instead
// and use CopyTo()
type Args struct {
	args  []argsKV
	buf   []byte
	bufKV argsKV
}

type argsKV struct {
	key   []byte
	value []byte
}

// Clear clears query args.
func (a *Args) Clear() {
	a.args = a.args[:0]
}

// CopyTo copies all args to dst.
func (a *Args) CopyTo(dst *Args) {
	dst.args = copyArgs(dst.args, a.args)
}

// VisitAll calls f for each existing arg.
//
// f must not retain references to key and value after returning.
// Make key and/or value copies if you need storing them after returning.
func (a *Args) VisitAll(f func(key, value []byte)) {
	visitArgs(a.args, f)
}

// Len returns the number of query args.
func (a *Args) Len() int {
	return len(a.args)
}

// Set sets 'key=value' argument.
func (a *Args) Set(key, value string) {
	a.bufKV.value = AppendBytesStr(a.bufKV.value[:0], value)
	a.SetBytesV(key, a.bufKV.value)
}

// SetBytesK sets 'key=value' argument.
//
// It is safe modifying key buffer after SetBytesK returns.
func (a *Args) SetBytesK(key []byte, value string) {
	a.bufKV.value = AppendBytesStr(a.bufKV.value[:0], value)
	a.SetBytesKV(key, a.bufKV.value)
}

// SetBytesV sets 'key=value' argument.
//
// It is safe modifying value buffer after SetBytesV return.
func (a *Args) SetBytesV(key string, value []byte) {
	a.bufKV.key = AppendBytesStr(a.bufKV.key[:0], key)
	a.SetBytesKV(a.bufKV.key, value)
}

// SetBytesKV sets 'key=value' argument.
//
// It is safe modifying key and value buffers after SetBytesKV return.
func (a *Args) SetBytesKV(key, value []byte) {
	a.args = setArg(a.args, key, value)
}

// Peek returns query arg value for the given key.
//
// Returned value is valid until the next Args call.
func (a *Args) Peek(key string) []byte {
	return peekArgStr(a.args, key)
}

// PeekBytes returns query arg value for the given key.
//
// Returned value is valid until the next Args call.
//
// It is safe modifying key buffer after PeekBytes return.
func (a *Args) PeekBytes(key []byte) []byte {
	return peekArgBytes(a.args, key)
}

// Has returns true if the given key exists in Args.
func (a *Args) Has(key string) bool {
	a.bufKV.key = AppendBytesStr(a.bufKV.key[:0], key)
	return a.HasBytes(a.bufKV.key)
}

// HasBytes returns true if the given key exists in Args.
func (a *Args) HasBytes(key []byte) bool {
	return hasArg(a.args, key)
}

// Del deletes argument with the given key from query args.
func (a *Args) Del(key string) {
	a.bufKV.key = AppendBytesStr(a.bufKV.key[:0], key)
	a.DelBytes(a.bufKV.key)
}

// DelBytes deletes argument with the given key from query args.
//
// It is safe modifying key buffer after DelBytes return.
func (a *Args) DelBytes(key []byte) {
	a.args = delArg(a.args, key)
}

// String returns string representation of query args.
func (a *Args) String() string {
	a.buf = a.AppendBytes(a.buf[:0])
	return string(a.buf)
}

// AppendBytes appends query string to dst and returns dst
// (which may be newly allocated).
func (a *Args) AppendBytes(dst []byte) []byte {
	for i, n := 0, len(a.args); i < n; i++ {
		kv := &a.args[i]
		dst = appendQuotedArg(dst, kv.key)
		if len(kv.value) > 0 {
			dst = append(dst, '=')
			dst = appendQuotedArg(dst, kv.value)
		}
		if i+1 < n {
			dst = append(dst, '&')
		}
	}
	return dst
}

// Parse parsed the given string containning query args.
func (a *Args) Parse(s string) {
	a.buf = AppendBytesStr(a.buf[:0], s)
	a.ParseBytes(a.buf)
}

// ParseBytes parses the given b containing query args.
//
// It is safe modifying b buffer contents after ParseBytes return.
func (a *Args) ParseBytes(b []byte) {
	a.Clear()
	var s argsScanner
	s.b = b

	var kv *argsKV
	a.args, kv = allocArg(a.args)
	for s.next(kv) {
		if len(kv.key) > 0 || len(kv.value) > 0 {
			a.args, kv = allocArg(a.args)
		}
	}
	a.args = releaseArg(a.args)
}

// ErrNoArgValue is returned when value with the given key is missing.
var ErrNoArgValue = errors.New("No valuf for the given key")

// GetUint returns uint value for the given key.
func (a *Args) GetUint(key string) (int, error) {
	value := a.Peek(key)
	if len(value) == 0 {
		return -1, ErrNoArgValue
	}
	return ParseUint(value)
}

// GetUintOrZero returns uint value for the given key.
//
// Zero(0) is returned on error.
func (a *Args) GetUintOrZero(key string) int {
	n, err := a.GetUint(key)
	if err != nil {
		n = 0
	}
	return n
}

// GetUfloat returns ufloat value for the given key.
func (a *Args) GetUfloat(key string) (float64, error) {
	value := a.Peek(key)
	if len(value) == 0 {
		return -1, ErrNoArgValue
	}
	return ParseUfloat(value)
}

// GetUfloatOrZero returns ufloat value for the given key.
//
// Zero(0) is returned on error.
func (a *Args) GetUfloatOrZero(key string) float64 {
	f, err := a.GetUfloat(key)
	if err != nil {
		f = 0
	}
	return f
}

//////////////////////////////////////////////////
// utilities
//////////////////////////////////////////////////

// AppendBytesStr appends src to dst and returns dst
// (which may be newly allocated)
func AppendBytesStr(dst []byte, src string) []byte {
	for i, n := 0, len(src); i < n; i++ {
		dst = append(dst, src[i])
	}
	return dst
}

// EqualBytesStr returns true if string(b) == s.
//
// It doesn't allocate memory unlike string(b) do
func EqualBytesStr(b []byte, s string) bool {
	if len(s) != len(b) {
		return false
	}
	for i, n := 0, len(s); i < n; i++ {
		if s[i] != b[i] {
			return false
		}
	}
	return true
}

//////////////////////////////////////////////////
// private functions
//////////////////////////////////////////////////

func appendQuotedArg(dst, v []byte) []byte {
	for _, c := range v {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '/' || c == '.' {
			dst = append(dst, c)
		} else {
			dst = append(dst, '%', hexChar(c>>4), hexChar(c&15))
		}
	}
	return dst
}

func hexChar(c byte) byte {
	if c < 10 {
		return '0' + c
	}
	return c - 10 + 'A'
}

func unhex(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	if c >= 'a' && c <= 'f' {
		return 10 + int(c-'a')
	}
	if c >= 'A' && c <= 'F' {
		return 10 + int(c-'A')
	}
	return -1
}

func decodeArg(dst, src []byte, decodePlus bool) []byte {
	dst = dst[:0]
	for i, n := 0, len(src); i < n; i++ {
		c := src[i]
		switch c {
		case '+':
			if decodePlus {
				c = ' '
			}
			dst = append(dst, c)
		case '%':
			if i+2 >= n {
				return append(dst, src[i:]...)
			}
			x1 := unhex(src[i+1])
			x2 := unhex(src[i+2])
			if x1 < 0 || x2 < 0 {
				dst = append(dst, c)
			} else {
				dst = append(dst, byte(x1<<4|x2))
				i += 2
			}
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

func visitArgs(args []argsKV, f func(k, v []byte)) {
	for i, n := 0, len(args); i < n; i++ {
		kv := &args[i]
		f(kv.key, kv.value)
	}
}

func copyArgs(dst, src []argsKV) []argsKV {
	if cap(dst) < len(src) {
		tmp := make([]argsKV, len(src))
		copy(tmp, dst)
		dst = tmp
	}
	n := len(src)
	dst = dst[:n]
	for i := 0; i < n; i++ {
		dstKV := &dst[i]
		srcKV := &src[i]
		dstKV.key = append(dstKV.key[:0], srcKV.key...)
		dstKV.value = append(dstKV.value[:0], srcKV.value...)
	}
	return dst
}

func delArg(args []argsKV, key []byte) []argsKV {
	for i, n := 0, len(args); i < n; i++ {
		kv := &args[i]
		if bytes.Equal(kv.key, key) {
			tmp := *kv
			copy(args[i:], args[i+1:])
			args[n-1] = tmp
			return args[:n-1]
		}
	}
	return args
}

func setArg(h []argsKV, key, value []byte) []argsKV {
	n := len(h)
	for i := 0; i < n; i++ {
		kv := &h[i]
		if bytes.Equal(kv.key, key) {
			kv.value = append(kv.value[:0], value...)
			return h
		}
	}

	if cap(h) > n {
		h = h[:n+1]
		kv := &h[n]
		kv.key = append(kv.key[:0], key...)
		kv.value = append(kv.value[:0], value...)
		return h
	}

	var kv argsKV
	kv.key = append(kv.key, key...)
	kv.value = append(kv.value, value...)
	return append(h, kv)
}

func peekArgBytes(h []argsKV, k []byte) []byte {
	for i, n := 0, len(h); i < n; i++ {
		kv := &h[i]
		if bytes.Equal(kv.key, k) {
			return kv.value
		}
	}
	return nil
}

func peekArgStr(h []argsKV, k string) []byte {
	for i, n := 0, len(h); i < n; i++ {
		kv := &h[i]
		if EqualBytesStr(kv.key, k) {
			return kv.value
		}
	}
	return nil
}

//////////////////////////////////////////////////
// argsParser
//////////////////////////////////////////////////

type argsScanner struct {
	b []byte
}

func (s *argsScanner) next(kv *argsKV) bool {
	if len(s.b) == 0 {
		return false
	}

	isKey := true
	k := 0
	for i, c := range s.b {
		switch c {
		case '=':
			if isKey {
				isKey = false
				kv.key = decodeArg(kv.key, s.b[:i], true)
				k = i + 1
			}
		case '&':
			if isKey {
				kv.key = decodeArg(kv.key, s.b[:i], true)
				kv.value = kv.value[:0]
			} else {
				kv.value = decodeArg(kv.value, s.b[k:i], true)
			}
			s.b = s.b[i+1:]
			return true
		}
	}

	if isKey {
		kv.key = decodeArg(kv.key, s.b, true)
		kv.value = kv.value[:0]
	} else {
		kv.value = decodeArg(kv.value, s.b[k:], true)
	}
	s.b = s.b[len(s.b):]
	return true
}

func hasArg(h []argsKV, k []byte) bool {
	for i, n := 0, len(h); i < n; i++ {
		kv := &h[i]
		if bytes.Equal(kv.key, k) {
			return true
		}
	}
	return false
}

func allocArg(h []argsKV) ([]argsKV, *argsKV) {
	n := len(h)
	if cap(h) > n {
		h = h[:n+1]
	} else {
		h = append(h, argsKV{})
	}
	return h, &h[n]
}

func releaseArg(h []argsKV) []argsKV {
	return h[:len(h)-1]
}
