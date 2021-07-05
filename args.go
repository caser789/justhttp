package fasthttp

type Args struct {
    args []argsKV
    buf []byte
}

type argsKV struct {
    key []byte
    value []byte
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

func (a *Args) String() string {
    a.buf = a.AppendBytes(a.buf[:0])
    return string(a.buf)
}

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
