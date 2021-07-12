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

var serverDate atomic.Value

func refreshServerDate() {
	b := AppendHTTPDate(nil, time.Now())
	serverDate.Store(b)
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
	// It may be negative:
	// -1 means Transfer-Encoding: chunked.
	// -2 means Transfer-Encoding: identity.
	ContentLength int

	contentType []byte
	server      []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV
}

// Len returns the number of headers set, not counting Coutent-Length,
// i.e. the number of times f is called in VisitAll.
func (h *ResponseHeader) Len() int {
	n := 0
	h.VisitAll(func(k, v []byte) { n++ })
	return n
}

// SetCookie sets the given response cookie.
//
// It is safe modifying cookie instance after the call.
func (h *ResponseHeader) SetCookie(cookie *Cookie) {
	h.bufKV.value = cookie.AppendBytes(h.bufKV.value[:0])
	h.cookies = setArg(h.cookies, cookie.Key, h.bufKV.value)
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
	dst.Clear()
	dst.StatusCode = h.StatusCode
	dst.ContentLength = h.ContentLength
	dst.ConnectionClose = h.ConnectionClose
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.server = append(dst.server[:0], h.server...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
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
	if len(h.cookies) > 0 {
		visitArgs(h.cookies, func(k, v []byte) {
			f(strSetCookie, v)
		})
	}
	visitArgs(h.h, f)
	if h.ConnectionClose {
		f(strConnection, strClose)
	}
}

// Clear clears response header.
func (h *ResponseHeader) Clear() {
	h.StatusCode = 0
	h.ContentLength = 0
	h.ConnectionClose = false

	h.server = h.server[:0]
	h.contentType = h.contentType[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]
}

// VisitAllCookie calls f for each response cookie.
//
// Cookie name is passed in key and the whole Set-Cookie header value
// is passed in value on each f invocation. Value may be parsed
// with Cookie.ParseBytes().
//
// f must not retain references to key and/or value after returning.
func (h *ResponseHeader) VisitAllCookie(f func(key, value []byte)) {
	visitArgs(h.cookies, f)
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
		writeHeaderLine(w, strTransferEncoding, strChunked)
	} else {
		writeContentLength(w, h.ContentLength)
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		writeHeaderLine(w, kv.key, kv.value)
	}

	n := len(h.cookies)
	if n > 0 {
		for i := 0; i < n; i++ {
			kv := &h.cookies[i]
			writeHeaderLine(w, strSetCookie, kv.value)
		}
	}

	if h.ConnectionClose {
		writeHeaderLine(w, strConnection, strClose)
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

	// parse protocol
	n := bytes.IndexByte(b, ' ')
	if n < 0 {
		return nil, fmt.Errorf("cannot find whitespace in the first line of response %q", buf)
	}
	if !bytes.Equal(b[:n], strHTTP11) {
		// Non-http/1.1 response. Close connection after it.
		h.ConnectionClose = true
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
	// 'identity' content-length by default
	h.ContentLength = -2

	var s headerScanner
	s.init(buf)
	var err error
	var kv *argsKV
	for s.next() {
		switch {
		case bytes.Equal(s.key, strContentType):
			h.contentType = append(h.contentType[:0], s.value...)
		case bytes.Equal(s.key, strServer):
			h.server = append(h.server[:0], s.value...)
		case bytes.Equal(s.key, strContentLength):
			if h.ContentLength != -1 {
				h.ContentLength, err = parseContentLength(s.value)
				if err != nil {
					if err == errNeedMore {
						return nil, err
					}
					return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", s.value, err, buf)
				}
			}
		case bytes.Equal(s.key, strTransferEncoding):
			if !bytes.Equal(s.value, strIdentity) {
				h.ContentLength = -1
			}
		case bytes.Equal(s.key, strConnection):
			if bytes.Equal(s.value, strClose) {
				h.ConnectionClose = true
			}
		case bytes.Equal(s.key, strSetCookie):
			h.cookies, kv = allocArg(h.cookies)
			kv.key = getCookieKey(kv.key, s.value)
			kv.value = append(kv.value[:0], s.value...)
		default:
			h.h, kv = allocArg(h.h)
			kv.key = append(kv.key[:0], s.key...)
			kv.value = append(kv.value[:0], s.value...)
		}
	}
	if s.err != nil {
		return nil, s.err
	}

	if len(h.contentType) == 0 {
		return nil, fmt.Errorf("missing required Content-Type header in %q", buf)
	}
	if h.ContentLength == -2 {
		// Close connection after 'identity' response.
		h.ConnectionClose = true
	}
	return s.b, nil
}

// Len returns the number of headers set, not counting Content-Length,
// i.e. the number of times f is called in VisitAll.
func (h *RequestHeader) Len() int {
	n := 0
	h.VisitAll(func(k, v []byte) { n++ })
	return n
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
	case bytes.Equal(strSetCookie, key):
		var kv *argsKV
		h.cookies, kv = allocArg(h.cookies)
		kv.key = getCookieKey(kv.key, value)
		kv.value = append(kv.value[:0], value...)
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

// GetCookie fills cookie for the given cookie.Key.
//
// Returns false if cookie with the given cookie.Key is missing
func (h *ResponseHeader) GetCookie(cookie *Cookie) bool {
	v := peekArg(h.cookies, cookie.Key)
	if v == nil {
		return false
	}
	cookie.ParseBytes(v)
	return true
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
	// It may be negative:
	// -1 means Transfer-Encoding: chunked.
	ContentLength int

	// Set to true if request contains 'Connection: close' header.
	ConnectionClose bool

	host        []byte
	contentType []byte
	userAgent   []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV

    // aux buffer for Client.
    clientBuf []byte
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

// VisitAllCookie calls f for each request cookie.
//
// f must not retain references to key and/or value after returning.
func (h *RequestHeader) VisitAllCookie(f func(key, value []byte)) {
	visitArgs(h.cookies, f)
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
	if len(h.userAgent) > 0 {
		f(strUserAgent, h.userAgent)
	}
	if len(h.cookies) > 0 {
		h.bufKV.value = appendRequestCookieBytes(h.bufKV.value[:0], h.cookies)
		f(strCookie, h.bufKV.value)
	}
	visitArgs(h.h, f)
	if h.ConnectionClose {
		f(strConnection, strClose)
	}
}

// CopyTo copies all the headers to dst.
func (h *RequestHeader) CopyTo(dst *RequestHeader) {
	dst.Clear()
	dst.Method = append(dst.Method[:0], h.Method...)
	dst.RequestURI = append(dst.RequestURI[:0], h.RequestURI...)
	dst.ContentLength = h.ContentLength
	dst.ConnectionClose = h.ConnectionClose
	dst.host = append(dst.host[:0], h.host...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.userAgent = append(dst.userAgent[:0], h.userAgent...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
}

// IsMethodGet returns true if request method is GET.
func (h *RequestHeader) IsMethodGet() bool {
	return bytes.Equal(h.Method, strGet) || len(h.Method) == 0
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
	h.ConnectionClose = false

	h.host = h.host[:0]
	h.contentType = h.contentType[:0]
	h.userAgent = h.userAgent[:0]

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
	case bytes.Equal(strUserAgent, key):
		h.userAgent = append(h.userAgent[:0], value...)
	case bytes.Equal(strContentLength, key):
		// Content-Length is managed automatically
	case bytes.Equal(strConnection, key):
		if bytes.Equal(strClose, value) {
			h.ConnectionClose = true
		}
		// skip other 'Connection' shit :)
	case bytes.Equal(strTransferEncoding, key):
		// Transfer-Encoding is managed automatically
	case bytes.Equal(strConnection, key):
		// Connection is managed automatically
	case bytes.Equal(strCookie, key):
		h.cookies = parseRequestCookies(h.cookies, value)
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
	case bytes.Equal(strUserAgent, key):
		return h.userAgent
	case bytes.Equal(strConnection, key):
		if h.ConnectionClose {
			return strClose
		}
		return nil
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
	n = bytes.LastIndexByte(b, ' ')
	if n < 0 {
		// no http protocol found. Close connection after the request.
		h.ConnectionClose = true
		n = len(b)
	} else if n == 0 {
		return nil, fmt.Errorf("RequestURI cannot be empty in %q", buf)
	} else if !bytes.Equal(b[n+1:], strHTTP11) {
		// non-http/1.1 protocol. Close connection after the request.
		h.ConnectionClose = true
	}
	h.RequestURI = append(h.RequestURI[:0], b[:n]...)

	return bNext, nil
}

func (h *RequestHeader) parseHeaders(buf []byte) ([]byte, error) {
	h.ContentLength = -2

	var s headerScanner
	s.init(buf)
	var err error
	var kv *argsKV
	for s.next() {
		switch {
		case bytes.Equal(s.key, strHost):
			h.host = append(h.host[:0], s.value...)
		case bytes.Equal(s.key, strUserAgent):
			h.userAgent = append(h.userAgent[:0], s.value...)
		case bytes.Equal(s.key, strContentType):
			h.contentType = append(h.contentType[:0], s.value...)
		case bytes.Equal(s.key, strContentLength):
			if h.ContentLength != -1 {
				h.ContentLength, err = parseContentLength(s.value)
				if err != nil {
					if err == errNeedMore {
						return nil, err
					}
					return nil, fmt.Errorf("cannot parse Content-Length %q: %s at %q", s.value, err, buf)
				}
			}
		case bytes.Equal(s.key, strTransferEncoding):
			if !bytes.Equal(s.value, strIdentity) {
				h.ContentLength = -1
			}
		case bytes.Equal(s.key, strConnection):
			if bytes.Equal(s.key, strConnection) {
				h.ConnectionClose = true
			}
		case bytes.Equal(s.key, strCookie):
			h.cookies = parseRequestCookies(h.cookies, s.value)
		default:
			h.h, kv = allocArg(h.h)
			kv.key = append(kv.key[:0], s.key...)
			kv.value = append(kv.value[:0], s.value...)
		}
	}
	if s.err != nil {
		return nil, s.err
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
	return s.b, nil
}

// Write writes request header to w.
func (h *RequestHeader) Write(w *bufio.Writer) error {
	method := h.Method
	if len(method) == 0 {
		method = strGet
	}
	w.Write(method)
	w.WriteByte(' ')

	requestURI := h.RequestURI
	if len(requestURI) == 0 {
		requestURI = strSlash
	}
	w.Write(requestURI)
	w.WriteByte(' ')
	w.Write(strHTTP11)
	w.Write(strCRLF)

	userAgent := h.userAgent
	if len(userAgent) == 0 {
		userAgent = defaultUserAgent
	}
	writeHeaderLine(w, strUserAgent, userAgent)

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
			writeHeaderLine(w, strTransferEncoding, strChunked)
		} else {
			writeContentLength(w, h.ContentLength)
		}
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		writeHeaderLine(w, kv.key, kv.value)
	}

	n := len(h.cookies)
	if n > 0 {
		h.bufKV.value = appendRequestCookieBytes(h.bufKV.value[:0], h.cookies)
		writeHeaderLine(w, strCookie, h.bufKV.value)
	}

	if h.ConnectionClose {
		writeHeaderLine(w, strConnection, strClose)
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

type headerScanner struct {
	headers []byte
	b       []byte
	key     []byte
	value   []byte
	err     error
	lineNum int
}

func (s *headerScanner) init(headers []byte) {
	s.headers = headers
	s.b = headers
	s.key = nil
	s.value = nil
	s.lineNum = 0
}

func (s *headerScanner) next() bool {
	var b []byte
	b, s.b, s.err = nextLine(s.b)
	if s.err != nil {
		return false
	}
	if len(b) == 0 {
		return false
	}

	s.lineNum++
	n := bytes.IndexByte(b, ':')
	if n < 0 {
		s.err = fmt.Errorf("cannot find colon at line #%d in %q", s.lineNum, s.headers)
		return false
	}
	s.key = b[:n]
	n++
	normalizeHeaderKey(s.key)
	for len(b) > n && b[n] == ' ' {
		n++
	}
	s.value = b[n:]
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
