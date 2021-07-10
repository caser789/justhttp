package fasthttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

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

// ResponseHeader represents HTTP response header.
//
// It is forbidden copying ResponseHeader instances.
// Create new instances instead and use CopyTo.
type ResponseHeader struct {
	// Set to tru if response contains 'Connection: close' header.
	ConnectionClose bool

	// Response status code.
	StatusCode int

	// Resposne content length read from Content-Length header.
	//
	// It may be negative on chunked response.
	ContentLength int

	contentType []byte
	server      []byte

	h     []argsKV
	bufKV argsKV
}

// SetBytesK sets the given 'key: value' header.
//
// It is safe modifying key buffer after SetBytesK return.
func (h *ResponseHeader) SetBytesK(key []byte, value string) {
	h.bufKV.value = AppendBytesStr(h.bufKV.value[:0], value)
	h.SetBytesKV(key, h.bufKV.value)
}

// SetBytesV sets the given 'key: value' header.
//
// It is safe modifying value buffer after SetBytesV return.
func (h *ResponseHeader) SetBytesV(key string, value []byte) {
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.SetCanonical(k, value)
}

// SetBytesKV sets the given 'key: value' header.
//
// It is safe modifying key and value buffers after SetBytesKV return.
func (h *ResponseHeader) SetBytesKV(key, value []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.SetCanonical(h.bufKV.key, value)
}

// Del deletes header with the given key.
func (h *ResponseHeader) Del(key string) {
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.h = delArg(h.h, k)
}

// DelBytes deletes header with the given key.
func (h *ResponseHeader) DelBytes(key []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.h = delArg(h.h, h.bufKV.key)
}

// CopyTo copies all the headers to dst.
func (h *ResponseHeader) CopyTo(dst *ResponseHeader) {
	dst.StatusCode = h.StatusCode
	dst.ContentLength = h.ContentLength
	dst.ConnectionClose = h.ConnectionClose
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.server = append(dst.server[:0], h.server...)
	dst.h = copyArgs(dst.h, h.h)
}

// VisitAll calls f for each header except Content-Length.
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *ResponseHeader) VisitAll(f func(key, value []byte)) {
	if len(h.contentType) > 0 {
		f(strContentType, h.contentType)
	}
	if len(h.server) > 0 {
		f(strServer, h.server)
	}
	if h.ConnectionClose {
		f(strConnection, strClose)
	}
	visitArgs(h.h, f)
}

// Clear clears response header.
func (h *ResponseHeader) Clear() {
	h.StatusCode = 0
	h.ContentLength = 0
	h.ConnectionClose = false

	h.server = h.server[:0]
	h.contentType = h.contentType[:0]

	h.h = h.h[:0]
}

// Write writs response header to w.
func (h *ResponseHeader) Write(w *bufio.Writer) error {
	statusCode := h.StatusCode
	if statusCode < 0 {
		return fmt.Errorf("response cannot have negative status code=%d", statusCode)
	}
	if statusCode == 0 {
		statusCode = StatusOK
	}
	w.Write(statusLine(statusCode))

	server := h.server
	if len(server) == 0 {
		server = defaultServerName
	}
	writeHeaderLine(w, strServer, server)
	writeHeaderLine(w, strDate, serverDate.Load().([]byte))

	contentType := h.contentType
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
		if !bytes.Equal(strServer, kv.key) && !bytes.Equal(strContentType, kv.key) {
			writeHeaderLine(w, kv.key, kv.value)
		}
	}

	_, err := w.Write(strCRLF)
	return err
}

// Read reads response header from r.
func (h *ResponseHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if err != errNeedMore {
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
		if err == errNeedMore && !isEOF {
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
		switch {
		case bytes.Equal(p.key, strContentType):
			h.contentType = append(h.contentType[:0], p.value...)
		case bytes.Equal(p.key, strServer):
			h.server = append(h.server[:0], p.value...)
		case bytes.Equal(p.key, strContentLength):
			if h.ContentLength != -1 {
				h.ContentLength, err = parseContentLength(p.value)
				if err != nil {
					if err == errNeedMore {
						return nil, err
					}
					return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", p.value, err, buf)
				}
			}
		case bytes.Equal(p.key, strTransferEncoding):
			if bytes.Equal(p.value, strChunked) {
				h.ContentLength = -1
			}
		case bytes.Equal(p.key, strConnection):
			if bytes.Equal(p.value, strClose) {
				h.ConnectionClose = true
			}
		default:
			h.h = setArg(h.h, p.key, p.value)
		}
	}
	if p.err != nil {
		return nil, p.err
	}

	if len(h.contentType) == 0 {
		return nil, fmt.Errorf("missing required Content-Type header in %q", buf)
	}
	if h.ContentLength == -2 {
		return nil, fmt.Errorf("missing both Content-Length and Transfer-Encoding: chunked in %q", buf)
	}
	return p.b, nil
}

// Set sets the given 'key: value' header.
func (h *RequestHeader) Set(key, value string) {
	initHeaderKV(&h.bufKV, key, value)
	h.SetCanonical(h.bufKV.key, h.bufKV.value)
}

// Set sets the given 'key: value' header.
func (h *ResponseHeader) Set(key, value string) {
	initHeaderKV(&h.bufKV, key, value)
	h.SetCanonical(h.bufKV.key, h.bufKV.value)
}

// SetCanonical sets the given 'key: value' header assuming that
// key is in canonical form.
//
// It is safe modifying key and value buffers after SetCanonical return.
func (h *ResponseHeader) SetCanonical(key, value []byte) {
	switch {
	case bytes.Equal(strContentType, key):
		h.contentType = append(h.contentType[:0], value...)
	case bytes.Equal(strServer, key):
		h.server = append(h.server[:0], value...)
	case bytes.Equal(strContentLength, key):
		// skip Content-Length setting, since it will be set automatically.
	case bytes.Equal(strConnection, key):
		if bytes.Equal(strClose, value) {
			h.ConnectionClose = true
		}
		// skip other 'Connection' shit :)
	case bytes.Equal(strTransferEncoding, key):
		// Transfer-Encoding is managed automatically.
	case bytes.Equal(strDate, key):
		// Date is managed automatically
	default:
		h.h = setArg(h.h, key, value)
	}
}

// SetBytesK sets the given 'key: value' header.
//
// It is safe modifying key buffer after SetBytesK return.
func (h *RequestHeader) SetBytesK(key []byte, value string) {
	h.bufKV.value = AppendBytesStr(h.bufKV.value[:0], value)
	h.SetBytesKV(key, h.bufKV.value)
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

// Get returns header value for the given key.
//
// Get allocates memory on each call, so prefer using Peek instead.
func (h *ResponseHeader) Get(key string) string {
	return string(h.Peek(key))
}

// GetBytes returns header value for the given key.
//
// GetBytes allocates memory on each call, so prefer using PeekBytes instead.
func (h *ResponseHeader) GetBytes(key []byte) string {
	return string(h.PeekBytes(key))
}

// Peek returns header value for the given key.
//
// Returned value is valid until the next call to ResponseHeader.
// Do not store references to returned value. Make copies instead.
func (h *ResponseHeader) Peek(key string) []byte {
	k := getHeaderKeyBytes(&h.bufKV, key)
	return h.peek(k)
}

// PeekBytes returns header value for the given key.
//
// Returned value is valid until the next call to ResponseHeader.
// Do not store references to returned value. Make copies instead.
func (h *ResponseHeader) PeekBytes(key []byte) []byte {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	return h.peek(h.bufKV.key)
}

func (h *ResponseHeader) peek(key []byte) []byte {
	switch {
	case bytes.Equal(strContentType, key):
		return h.contentType
	case bytes.Equal(strServer, key):
		return h.server
	case bytes.Equal(strConnection, key):
		if h.ConnectionClose {
			return strClose
		}
		return nil
	default:
		return peekArg(h.h, key)
	}
}

// RequestHeader represents HTTP request header.
//
// It is forbidden copying RequestHeader instances.
// Create new instances instead and use CopyTo.
type RequestHeader struct {
	// Request method (e.g. 'GET', 'POST', etc.).
	Method []byte

	// Request URI read from the first request line.
	RequestURI []byte

	// Request content length read from Content-Length header.
	//
	// It may be negative on chunked request.
	ContentLength int

	host        []byte
	contentType []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV
}

// Del deletes header with the given key.
func (h *RequestHeader) Del(key string) {
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.h = delArg(h.h, k)
}

// DelBytes deletes header with the given key.
func (h *RequestHeader) DelBytes(key []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.h = delArg(h.h, h.bufKV.key)
}

// VisitAll calls f for each header except Conten-Length.
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *RequestHeader) VisitAll(f func(key, value []byte)) {
	if len(h.host) > 0 {
		f(strHost, h.host)
	}
	if len(h.contentType) > 0 {
		f(strContentType, h.contentType)
	}
	visitArgs(h.h, f)
}

// CopyTo copies all the headers to dst.
func (h *RequestHeader) CopyTo(dst *RequestHeader) {
	dst.Method = append(dst.Method[:0], h.Method...)
	dst.RequestURI = append(dst.RequestURI[:0], h.RequestURI...)
	dst.ContentLength = h.ContentLength
	dst.host = append(dst.host[:0], h.host...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.h = copyArgs(dst.h, h.h)
}

// IsMethodGet returns true if request method is GET.
func (h *RequestHeader) IsMethodGet() bool {
	return bytes.Equal(h.Method, strGet)
}

// IsMethodPost returns true if request method is POST.
func (h *RequestHeader) IsMethodPost() bool {
	return bytes.Equal(h.Method, strPost)
}

// IsMethodHead returns true if request method is HEAD.
func (h *RequestHeader) IsMethodHead() bool {
	return bytes.Equal(h.Method, strHead)
}

// SetCookie sets 'key: value' cookies.
func (h *RequestHeader) SetCookie(key, value string) {
	h.bufKV.key = AppendBytesStr(h.bufKV.key[:0], key)
	h.SetCookieBytesK(h.bufKV.key, value)
}

// SetCookieBytesK sets 'key: value' cookies
//
// It is safe modifying key buffer after SetCookieBytesK call.
func (h *RequestHeader) SetCookieBytesK(key []byte, value string) {
	h.bufKV.value = AppendBytesStr(h.bufKV.value[:0], value)
	h.SetCookieBytesKV(key, h.bufKV.value)
}

// SetCookieBytesKV sets 'key: value' cookies.
//
// It is safe modifying key and value buffers after SetCookieBytesKV call.
func (h *RequestHeader) SetCookieBytesKV(key, value []byte) {
	h.cookies = setArg(h.cookies, key, value)
}

// Clear clears request header
func (h *RequestHeader) Clear() {
	h.Method = h.Method[:0]
	h.RequestURI = h.RequestURI[:0]
	h.ContentLength = 0

	h.host = h.host[:0]
	h.contentType = h.contentType[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]
}

// SetBytesV sets the given 'key: value' header.
//
// It is safe modifying value buffer after SetBytesV return.
func (h *RequestHeader) SetBytesV(key string, value []byte) {
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.SetCanonical(k, value)
}

// SetBytesKV sets the given 'key: value' header.
//
// It is safe modifying key and value buffers after SetBytesKV return.
func (h *RequestHeader) SetBytesKV(key, value []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.SetCanonical(h.bufKV.key, value)
}

// SetCanonical sets the given 'key: value' header assuming that
// key is in canonical form.
//
// It is safe modifying key and value buffers after SetCanonical return.
func (h *RequestHeader) SetCanonical(key, value []byte) {
	switch {
	case bytes.Equal(strHost, key):
		h.host = append(h.host[:0], value...)
	case bytes.Equal(strContentType, key):
		h.contentType = append(h.contentType[:0], value...)
	case bytes.Equal(strContentLength, key):
		// Content-Length is managed automatically
	case bytes.Equal(strTransferEncoding, key):
		// Transfer-Encoding is managed automatically
	case bytes.Equal(strConnection, key):
		// Connection is managed automatically
	default:
		h.h = setArg(h.h, key, value)
	}
}

// PeekCookie returns cookie for the given key
func (h *RequestHeader) PeekCookie(key string) []byte {
    h.bufKV.key = AppendBytesStr(h.bufKV.key[:0], key)
    return h.PeekCookieBytes(h.bufKV.key)
}

// PeekCookieBytes returns cookie for the given key
func (h *RequestHeader) PeekCookieBytes(key []byte) []byte {
    return peekArg(h.cookies, key)
}

// Peek returns header value for the given key.
//
// Returned value is valid until the next call to RequestHeader.
// Do not store references to returned value. Make copies instead.
func (h *RequestHeader) Peek(key string) []byte {
	k := getHeaderKeyBytes(&h.bufKV, key)
	return h.peek(k)
}

// PeekBytes returns header value for the given key.
//
// Returned value is valid until the next call to RequestHandler.
// Do not store references to returned value. Make copies instead.
func (h *RequestHeader) PeekBytes(key []byte) []byte {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	return h.peek(h.bufKV.key)
}

func (h *RequestHeader) peek(key []byte) []byte {
	switch {
	case bytes.Equal(strHost, key):
		return h.host
	case bytes.Equal(strContentType, key):
		return h.contentType
	default:
		return peekArg(h.h, key)
	}
}

// GetCookie returns cookie for the given key.
//
// GetCookie allocates memory on each call, so prefer using PeekCookie instead.
func (h *RequestHeader) GetCookie(key string) string {
    return string(h.PeekCookie(key))
}

// GetCookieBytes returns cookie for the given key.
//
// GetCookieBytes allocated memory on each call, so prefer using PeekCookieBytes
// instead.
func (h *RequestHeader) GetCookieBytes(key []byte) string {
    return string(h.PeekCookieBytes(key))
}

// Get returns header value for the given key.
//
// Get allocates memory on each call, so prefer using Peek instead.
func (h *RequestHeader) Get(key string) string {
	return string(h.Peek(key))
}

// GetBytes returns header value for the given key.
//
// GetBytes allocates memory on each call, so prefer using PeekBytes instead.
func (h *RequestHeader) GetBytes(key []byte) string {
	return string(h.PeekBytes(key))
}

// Read reads request header from r.
func (h *RequestHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if err != errNeedMore {
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
		if err == errNeedMore && !isEOF {
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
		switch {
		case bytes.Equal(p.key, strHost):
			h.host = append(h.host[:0], p.value...)
		case bytes.Equal(p.key, strContentType):
			h.contentType = append(h.contentType[:0], p.value...)
		case bytes.Equal(p.key, strContentLength):
			if h.ContentLength != -1 {
				h.ContentLength, err = parseContentLength(p.value)
				if err != nil {
					if err == errNeedMore {
						return nil, err
					}
					return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", p.value, err, buf)
				}
			}
		case bytes.Equal(p.key, strTransferEncoding):
			if bytes.Equal(p.value, strChunked) {
				h.ContentLength = -1
			}
		case bytes.Equal(p.key, strCookie):
			h.cookies = parseCookies(h.cookies, p.value, &h.bufKV)
		default:
			h.h = setArg(h.h, p.key, p.value)
		}
	}
	if p.err != nil {
		return nil, p.err
	}

	if len(h.host) == 0 {
		return nil, fmt.Errorf("missing required Host header in %q", buf)
	}

	if h.IsMethodPost() {
		if len(h.contentType) == 0 {
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

// Write writes request header to w.
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

	host := h.host
	if len(host) == 0 {
		return fmt.Errorf("missing required Host header")
	}
	writeHeaderLine(w, strHost, host)

	if h.IsMethodPost() {
		contentType := h.contentType
		if len(contentType) == 0 {
			return fmt.Errorf("missing required Content-Type header for POST request")
		}
		writeHeaderLine(w, strContentType, contentType)
		if h.ContentLength < 0 {
			return fmt.Errorf("missing required Content-Length header for POST request")
		}
		writeContentLength(w, h.ContentLength)
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		if !bytes.Equal(strHost, kv.key) && !bytes.Equal(strContentType, kv.key) {
			writeHeaderLine(w, kv.key, kv.value)
		}
	}

	n := len(h.cookies)
	if n > 0 {
		h.bufKV.value = appendCookieBytes(h.bufKV.value[:0], h.cookies)
		writeHeaderLine(w, strCookie, h.bufKV.value)
	}

	_, err := w.Write(strCRLF)
	return err
}

//////////////////////////////////////////////////
// helpers
//////////////////////////////////////////////////
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
		return nil, nil, errNeedMore
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

func initHeaderKV(kv *argsKV, key, value string) {
	kv.key = getHeaderKeyBytes(kv, key)
	kv.value = AppendBytesStr(kv.value[:0], value)
}

func getHeaderKeyBytes(kv *argsKV, key string) []byte {
	kv.key = AppendBytesStr(kv.key[:0], key)
	normalizeHeaderKey(kv.key)
	return kv.key
}

var errNeedMore = errors.New("need more data: cannot find trailing lf")
