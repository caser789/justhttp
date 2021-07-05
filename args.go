package fasthttp

import (
    "bytes"
)

type Args struct {
    args []argsKV
    buf []byte
}

type argsKV struct {
    key []byte
    value []byte
}

func (a *Args) Clear() {
    a.args = a.args[:0]
}

func (a *Args) Len() int {
    return len(a.args)
}

func (a *Args) Set(key, value string) {
    a.buf = CopyBytesStr(a.buf, value)
    a.SetBytes(key, a.buf)
}

// Why not turn the key into []byte?
func (a *Args) SetBytes(key string, value []byte) {
    n := len(a.args)
    // in case the key exists
    for i := 0; i < n; i++ {
        kv := &a.args[i]
        if EqualBytesStr(kv.key, key) {
            kv.value = append(kv.value[:0], value...)
            return
        }
    }

    if cap(a.args) > n {
        a.args = a.args[:n+1]
        kv := &a.args[n]
        kv.key = CopyBytesStr(kv.key, key)
        kv.value = append(kv.value[:0], value...)
        return
    }

    var kv argsKV
    kv.key = CopyBytesStr(kv.key, key)
    kv.value = append(kv.value, value...)
    a.args = append(a.args, kv)
}

// Warning: each call allocates memory for returned string.
func (a *Args) Get(key string) string {
    return string(a.Peek(key))
}

func (a *Args) GetBytes(dst []byte, key string) []byte {
    value := a.Peek(key)
    return append(dst[:0], value...)
}

func (a *Args) Peek(key string) []byte {
    for i, n := 0, len(a.args); i < n; i++ {
        kv := &a.args[i]
        if EqualBytesStr(kv.key, key) {
            return kv.value
        }
    }
    return nil
}

// Get query string
func (a *Args) String() string {
    a.buf = a.AppendBytes(a.buf[:0])
    return string(a.buf)
}

// Query string in []byte
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

// From query string
func (a *Args) Parse(s string) {
    a.buf = CopyBytesStr(a.buf, s)
    a.ParseBytes(a.buf)
}

// From query string in []byte
func (a *Args) ParseBytes(b []byte) {
    a.Clear()
    var p argsParser
    p.Init(b)

    n := cap(a.args)
    a.args = a.args[:n]

    // Remove non-exists keys
    for i := 0; i < n; i++ {
        kv := &a.args[i]
        if !p.Next(kv) {
            for j := 0; j < i; j++ {
            }
            a.args = a.args[:i]
            return
        }
    }

    for {
        var kv argsKV
        if !p.Next(&kv) {
            return
        }
        a.args = append(a.args, kv)
    }
}

//////////////////////////////////////////////////
// utilities
//////////////////////////////////////////////////

// Copy string into a byte array
func CopyBytesStr(dst []byte, src string) []byte {
    dst = dst[:0]
    for i, n := 0, len(src); i < n; i++ {
        dst = append(dst, src[i])
    }
    return dst
}

// check if a string equal a byte array
func EqualBytesStr(b []byte, s string) bool {
    if len(s) != len(b) {
        return false
    }
    for i, n := 0, len(s); i<n; i++ {
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
        if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '/' {
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

//////////////////////////////////////////////////
// argsParser
//////////////////////////////////////////////////

type argsParser struct {
    b []byte
}

func (p *argsParser) Init(buf []byte) {
    p.b = buf
}

// Fill in the content, or return false
func (p *argsParser) Next(kv *argsKV) bool {
    for {
        if !p.next(kv) {
            return false
        }
        if len(kv.key) > 0 || len(kv.value) > 0 {
            if kv.key == nil {
                kv.key = []byte{}
            }
            if kv.value == nil {
                kv.value = []byte{}
            }
            return true
        }
    }
}

func (p *argsParser) next(kv *argsKV) bool {
    if len(p.b) == 0 {
        return false
    }

    n := bytes.IndexByte(p.b, '&')
    var b []byte
    if n < 0 {
        b = p.b
        p.b = p.b[len(p.b):]
    } else {
        b = p.b[:n]
        p.b = p.b[n+1:]
    }

    n = bytes.IndexByte(b, '=')
    if n < 0 {
        kv.key = decodeArg(kv.key[:0], b, true)
        kv.value = kv.value[:0]
    } else {
        kv.key = decodeArg(kv.key[:0], b[:n], true)
        kv.value = decodeArg(kv.value[:0], b[n+1:], true)
    }
    return true
}
