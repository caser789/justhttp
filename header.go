package fasthttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

var (
	defaultServerName  = []byte("fasthttp server")
	defaultContentType = []byte("text/plain; charset=utf-8")
)

var (
	strSlash           = []byte("/")
	strCRLF            = []byte("\r\n")
	strHTTP            = []byte("http")
	strHTTP11          = []byte("HTTP/1.1")
	strColonSlashSlash = []byte("://")
	strColonSpace      = []byte(": ")

	strGet  = []byte("GET")
	strHead = []byte("HEAD")
	strPost = []byte("POST")

	strConnection       = []byte("Connection")
	strContentLength    = []byte("Content-Length")
	strContentType      = []byte("Content-Type")
	strDate             = []byte("Date")
	strHost             = []byte("Host")
	strReferer          = []byte("Referer")
	strServer           = []byte("Server")
	strTransferEncoding = []byte("Transfer-Encoding")
	strUserAgent        = []byte("User-Agent")

	strClose               = []byte("close")
	strChunked             = []byte("chunked")
	strPostArgsContentType = []byte("application/x-www-form-urlencoded")
)

// Atomic values

var statusLines atomic.Value

func init() {
	statusLines.Store(make(map[int][]byte))
}

func init() {
	refreshServerDate()
	go func() {
		for {
			time.Sleep(time.Second)
			refreshServerDate()
		}
	}()
}

var (
	serverDate  atomic.Value
	gmtLocation = func() *time.Location {
		x, err := time.LoadLocation("GMT")
		if err != nil {
			panic(fmt.Sprintf("cannot load GMT location: %s", err))
		}
		return x
	}()
)

func refreshServerDate() {
	s := time.Now().In(gmtLocation).Format(time.RFC1123)
	serverDate.Store([]byte(s))
}

// ResponseHeader
type ResponseHeader struct {
	ConnectionClose bool
	StatusCode      int
	Server          []byte
	ContentType     []byte
	ContentLength   int

	h     []argsKV
	bufKV argsKV
}

func (h *ResponseHeader) Clear() {
	h.StatusCode = 0
	h.ContentLength = 0
	h.ContentType = h.ContentType[:0]
	h.Server = h.Server[:0]
	h.ConnectionClose = false

	h.h = h.h[:0]
}

func (h *ResponseHeader) Write(w *bufio.Writer) error {
	statusCode := h.StatusCode
	if statusCode < 0 {
		return fmt.Errorf("response cannot have negative status code=%d", statusCode)
	}
	if statusCode == 0 {
		statusCode = 200
	}
	w.Write(statusLine(statusCode))

	server := h.Server
	if len(server) == 0 {
		server = defaultServerName
	}
	writeHeaderLine(w, strServer, server)
	writeHeaderLine(w, strDate, serverDate.Load().([]byte))

	contentType := h.ContentType
	if len(contentType) == 0 {
		contentType = defaultContentType
	}
	writeHeaderLine(w, strContentType, contentType)

	if h.ContentLength < 0 {
		return fmt.Errorf("missing required Content-Length header")
	}
	writeContentLength(w, h.ContentLength)

	if h.ConnectionClose {
		writeHeaderLine(w, strConnection, strClose)
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		writeHeaderLine(w, kv.key, kv.value)
	}

	_, err := w.Write(strCRLF)
	return err
}

func (h *ResponseHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if !isNeedMoreError(err) {
			h.Clear()
			return err
		}
		n = r.Buffered() + 1
	}
}

func (h *ResponseHeader) tryRead(r *bufio.Reader, n int) error {
	h.Clear()
	b, err := r.Peek(n)
	if len(b) == 0 {
		if err == io.EOF {
			return err
		}
		if err == nil {
			panic("bufio.Reader.Peek() returned nil, nil")
		}
		return fmt.Errorf("error when reading response headers: %s", err)
	}
	isEOF := (err != nil)
	b = mustPeekBuffered(r)
	bLen := len(b)
	if b, err = h.parse(b); err != nil {
		if isNeedMoreError(err) && !isEOF {
			return err
		}
		return fmt.Errorf("erorr when reading response headers: %s", err)
	}
	headersLen := bLen - len(b)
	mustDiscard(r, headersLen)
	return nil
}

func (h *ResponseHeader) parse(buf []byte) (b []byte, err error) {
	b, err = h.parseFirstLine(buf)
	if err != nil {
		return nil, err
	}
	return h.parseHeaders(b)
}

func (h *ResponseHeader) parseFirstLine(buf []byte) (b []byte, err error) {
	bNext := buf
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return nil, err
		}
	}

	// skip protocol
	n := bytes.IndexByte(b, ' ')
	if n < 0 {
		return nil, fmt.Errorf("cannot find whitespace in the first line of response %q", buf)
	}
	b = b[n+1:]

	// parse status code
	h.StatusCode, n, err = parseUintBuf(b)
	if err != nil {
		return nil, fmt.Errorf("cannot parse response status code: %s. Response %q", err, buf)
	}
	if len(b) > n && b[n] != ' ' {
		return nil, fmt.Errorf("unexpected char at the end of status code. Response %q", buf)
	}

	return bNext, nil
}

func (h *ResponseHeader) parseHeaders(buf []byte) ([]byte, error) {
	h.ContentLength = -2

	var p headerParser
	p.init(buf)
	var err error
	for p.next() {
		if bytes.Equal(p.key, strContentType) {
			h.ContentType = append(h.ContentType[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strContentLength) && h.ContentLength != -1 {
			h.ContentLength, err = parseContentLength(p.value)
			if err != nil {
				if isNeedMoreError(err) {
					return nil, err
				}
				return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", p.value, err, buf)
			}
			continue
		}
		if bytes.Equal(p.key, strTransferEncoding) && bytes.Equal(p.value, strChunked) {
			h.ContentLength = -1
			continue
		}
		if bytes.Equal(p.key, strServer) {
			h.Server = append(h.Server[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strConnection) && bytes.Equal(p.value, strClose) {
			h.ConnectionClose = true
			continue
		}
		h.setBytes(p.key, p.value)
	}
	if p.err != nil {
		return nil, p.err
	}

	if len(h.ContentType) == 0 {
		return nil, fmt.Errorf("missing required Content-Type header in %q", buf)
	}
	if h.ContentLength == -2 {
		return nil, fmt.Errorf("missing both Content-Length and Transfer-Encoding: chunked in %q", buf)
	}
	return p.b, nil
}

func (h *ResponseHeader) Set(key, value string) {
	h.bufKV.key = AppendBytesStr(h.bufKV.key[:0], key)
	normalizeHeaderKey(h.bufKV.key)
	k := h.bufKV.key

	switch {
	case bytes.Equal(strContentLength, k):
		// skip Content-Length setting, since it will be set automatically.
		return
	case bytes.Equal(strContentType, k):
		h.ContentType = AppendBytesStr(h.ContentType[:0], value)
		return
	case bytes.Equal(strServer, k):
		h.Server = AppendBytesStr(h.Server[:0], value)
		return
	case bytes.Equal(strConnection, k):
		if EqualBytesStr(strClose, value) {
			h.ConnectionClose = true
		}
		// skip other 'Connection' shit :)
		return
	case bytes.Equal(strTransferEncoding, k):
		// skip Transfer-Encoding shit, since we control it ourselves
		return
	}

	h.bufKV.value = AppendBytesStr(h.bufKV.value[:0], value)
	h.setBytes(k, h.bufKV.value)
}

func (h *ResponseHeader) setBytes(key, value []byte) {
	n := len(h.h)
	for i := 0; i < n; i++ {
		kv := &h.h[i]
		if bytes.Equal(kv.key, key) {
			kv.value = append(kv.value[:0], value...)
			return
		}
	}

	if cap(h.h) > n {
		h.h = h.h[:n+1]
		kv := &h.h[n]
		kv.key = append(kv.key[:0], key...)
		kv.value = append(kv.value[:0], value...)
		return
	}

	var kv argsKV
	kv.key = append(kv.key, key...)
	kv.value = append(kv.value, value...)
	h.h = append(h.h, kv)
}

func (h *ResponseHeader) Get(key string) string {
	return string(h.Peek(key))
}

func (h *ResponseHeader) Peek(key string) []byte {
	h.bufKV.key = AppendBytesStr(h.bufKV.key[:0], key)
	normalizeHeaderKey(h.bufKV.key)
	k := h.bufKV.key

	switch {
	case bytes.Equal(strContentType, k):
		return h.ContentType
	case bytes.Equal(strServer, k):
		return h.Server
	case bytes.Equal(strConnection, k):
		if h.ConnectionClose {
			return strClose
		}
		return nil
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		if bytes.Equal(k, kv.key) {
			return kv.value
		}
	}
	return nil
}

// RequestHeader
type RequestHeader struct {
	Method        []byte
	RequestURI    []byte
	Host          []byte
	UserAgent     []byte
	Referer       []byte
	ContentType   []byte
	ContentLength int
}

func (h *RequestHeader) IsMethodGet() bool {
	return bytes.Equal(h.Method, strGet)
}

func (h *RequestHeader) IsMethodPost() bool {
	return bytes.Equal(h.Method, strPost)
}

func (h *RequestHeader) IsMethodHead() bool {
	return bytes.Equal(h.Method, strHead)
}

func (h *RequestHeader) Clear() {
	h.Method = h.Method[:0]
	h.RequestURI = h.RequestURI[:0]
	h.Host = h.Host[:0]
	h.UserAgent = h.UserAgent[:0]
	h.Referer = h.Referer[:0]
	h.ContentType = h.ContentType[:0]
	h.ContentLength = 0
}

func (h *RequestHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if !isNeedMoreError(err) {
			h.Clear()
			return err
		}
		n = r.Buffered() + 1
	}
}

func (h *RequestHeader) tryRead(r *bufio.Reader, n int) error {
	h.Clear()
	b, err := r.Peek(n)
	if len(b) == 0 {
		if err == io.EOF {
			return err
		}
		if err == nil {
			panic("bufio.Reader.Peek() returned nil, nil")
		}
		return fmt.Errorf("error when reading request headers: %s", err)
	}
	isEOF := (err != nil)
	b = mustPeekBuffered(r)
	bLen := len(b)
	if b, err = h.parse(b); err != nil {
		if isNeedMoreError(err) && !isEOF {
			return err
		}
		return fmt.Errorf("error when reading request headers: %s", err)
	}
	headersLen := bLen - len(b)
	mustDiscard(r, headersLen)
	return nil
}

func (h *RequestHeader) parse(buf []byte) (b []byte, err error) {
	b, err = h.parseFirstLine(buf)
	if err != nil {
		return nil, err
	}
	return h.parseHeaders(b)
}

func (h *RequestHeader) parseFirstLine(buf []byte) (b []byte, err error) {
	bNext := buf
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return nil, err
		}
	}

	// parse method
	n := bytes.IndexByte(b, ' ')
	if n <= 0 {
		return nil, fmt.Errorf("cannot find http request method in %q", buf)
	}
	h.Method = append(h.Method[:0], b[:n]...)
	b = b[n+1:]

	// parse requestURI
	n = bytes.IndexByte(b, ' ')
	if n < 0 {
		n = len(b)
	} else if n == 0 {
		return nil, fmt.Errorf("RequestURI cannot be empty in %q", buf)
	}
	h.RequestURI = append(h.RequestURI[:0], b[:n]...)

	return bNext, nil
}

func (h *RequestHeader) parseHeaders(buf []byte) ([]byte, error) {
	h.ContentLength = -2

	var p headerParser
	p.init(buf)
	var err error
	for p.next() {
		if bytes.Equal(p.key, strHost) {
			h.Host = append(h.Host[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strUserAgent) {
			h.UserAgent = append(h.UserAgent[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strReferer) {
			h.Referer = append(h.Referer[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strContentType) {
			h.ContentType = append(h.ContentType[:0], p.value...)
			continue
		}
		if bytes.Equal(p.key, strContentLength) && h.ContentLength != -1 {
			h.ContentLength, err = parseContentLength(p.value)
			if err != nil {
				if isNeedMoreError(err) {
					return nil, err
				}
				return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", p.value, err, buf)
			}
			continue
		}
		if bytes.Equal(p.key, strTransferEncoding) && bytes.Equal(p.value, strChunked) {
			h.ContentLength = -1
			continue
		}
	}
	if p.err != nil {
		return nil, p.err
	}

	if len(h.Host) == 0 {
		return nil, fmt.Errorf("missing required Host header in %q", buf)
	}

	if h.IsMethodPost() {
		if len(h.ContentType) == 0 {
			return nil, fmt.Errorf("missing Content-Type for POST header in %q", buf)
		}
		if h.ContentLength == -2 {
			return nil, fmt.Errorf("missing Content-Length for POST header in %q", buf)
		}
	} else {
		h.ContentLength = 0
	}
	return p.b, nil
}

func (h *RequestHeader) Write(w *bufio.Writer) error {
	method := h.Method
	if len(method) == 0 {
		method = strGet
	}
	w.Write(method)
	w.WriteByte(' ')
	if len(h.RequestURI) == 0 {
		return fmt.Errorf("missing required RequestURI")
	}
	w.Write(h.RequestURI)
	w.WriteByte(' ')
	w.Write(strHTTP11)
	w.Write(strCRLF)

	if len(h.UserAgent) > 0 {
		writeHeaderLine(w, strUserAgent, h.UserAgent)
	}
	if len(h.Referer) > 0 {
		writeHeaderLine(w, strReferer, h.Referer)
	}

	if len(h.Host) == 0 {
		return fmt.Errorf("missing required Host header")
	}
	writeHeaderLine(w, strHost, h.Host)

	if h.IsMethodPost() {
		if len(h.ContentType) == 0 {
			return fmt.Errorf("missing required Content-Type header for POST request")
		}
		writeHeaderLine(w, strContentType, h.ContentType)
		if h.ContentLength < 0 {
			return fmt.Errorf("missing required Content-Length header for POST request")
		}
		writeContentLength(w, h.ContentLength)
	}

	_, err := w.Write(strCRLF)
	return err
}

//////////////////////////////////////////////////
// helpers
//////////////////////////////////////////////////

func statusLine(statusCode int) []byte {
	m := statusLines.Load().(map[int][]byte)
	h := m[statusCode]
	if h != nil {
		return h
	}

	statusText := "Error"
	switch statusCode {
	case 200:
		statusText = "OK"
	case 500:
		statusText = "Internal server error"
	}

	h = []byte(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, statusText))
	newM := make(map[int][]byte, len(m)+1)
	for k, v := range m {
		newM[k] = v
	}
	newM[statusCode] = h
	statusLines.Store(newM)
	return h
}

func writeHeaderLine(w *bufio.Writer, key, value []byte) {
	w.Write(key)
	w.Write(strColonSpace)
	w.Write(value)
	w.Write(strCRLF)
}

func writeContentLength(w *bufio.Writer, contentLength int) {
	w.Write(strContentLength)
	w.Write(strColonSpace)
	writeInt(w, contentLength)
	w.Write(strCRLF)
}

// parser

func mustPeekBuffered(r *bufio.Reader) []byte {
	buf, err := r.Peek(r.Buffered())
	if len(buf) == 0 || err != nil {
		panic(fmt.Sprintf("bufio.Reader.Peek() returned unexpected data (%q, %v)", buf, err))
	}
	return buf
}

func mustDiscard(r *bufio.Reader, n int) {
	if _, err := r.Discard(n); err != nil {
		panic(fmt.Sprintf("bufio.Reader.Discard(%d) failed: %s", n, err))
	}
}

func nextLine(b []byte) ([]byte, []byte, error) {
	nNext := bytes.IndexByte(b, '\n')
	if nNext < 0 {
		return nil, nil, needMoreError("cannot find lf in the %q", b)
	}
	n := nNext
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	return b[:n], b[nNext+1:], nil
}

type headerParser struct {
	headers []byte
	b       []byte
	key     []byte
	value   []byte
	err     error
	lineNum int
}

func (p *headerParser) init(headers []byte) {
	p.headers = headers
	p.b = headers
	p.key = nil
	p.value = nil
	p.lineNum = 0
}

func (p *headerParser) next() bool {
	var b []byte
	b, p.b, p.err = nextLine(p.b)
	if p.err != nil {
		return false
	}
	if len(b) == 0 {
		return false
	}

	p.lineNum++
	n := bytes.IndexByte(b, ':')
	if n < 0 {
		p.err = fmt.Errorf("cannot find colon at line #%d in %q", p.lineNum, p.headers)
		return false
	}
	p.key = b[:n]
	n++
	normalizeHeaderKey(p.key)
	for len(b) > n && b[n] == ' ' {
		n++
	}
	p.value = b[n:]
	return true
}

func parseContentLength(b []byte) (int, error) {
	v, n, err := parseUintBuf(b)
	if err != nil {
		return -1, err
	}
	if n != len(b) {
		return -1, fmt.Errorf("Non-numeric chars at the end of Content-Length")
	}
	return v, nil
}

func normalizeHeaderKey(b []byte) {
	n := len(b)
	up := true
	for i := 0; i < n; i++ {
		switch b[i] {
		case '-':
			up = true
		default:
			if up {
				up = false
				uppercaseByte(&b[i])
			} else {
				lowercaseByte(&b[i])
			}
		}
	}
}

// errors
type errNeedMore struct {
	s string
}

func (e *errNeedMore) Error() string {
	return e.s
}

func needMoreError(format string, args ...interface{}) error {
	return &errNeedMore{
		s: "need more data: " + fmt.Sprintf(format, args...),
	}
}

func isNeedMoreError(err error) bool {
	_, ok := err.(*errNeedMore)
	return ok
}
