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
	connectionClose bool

	// Response status code.
	StatusCode int

	contentLength      int
	contentLengthBytes []byte

	contentType []byte
	server      []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV

	rawHeaders       []byte
	rawHeadersParsed bool
}

// ConnectionClose returns true if 'Connection: close' header is set.
func (h *ResponseHeader) ConnectionClose() bool {
	h.parseRawHeaders()
	return h.connectionClose
}

// SetConnectionClose sets 'Connection: close' header.
func (h *ResponseHeader) SetConnectionClose() {
	// h.parseRawHeaders() isn't called for performance reasons.
	h.connectionClose = true
}

// ContentLength returns Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
// -2 means Transfer-Encoding: identity.
func (h *ResponseHeader) ContentLength() int {
	h.parseRawHeaders()
	return h.contentLength
}

// SetContentLength sets Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
// -2 means Transfer-Encoding: identity.
func (h *ResponseHeader) SetContentLength(contentLength int) {
	h.parseRawHeaders()
	h.contentLength = contentLength
	if contentLength >= 0 {
		h.contentLengthBytes = AppendUint(h.contentLengthBytes[:0], contentLength)
		h.h = delArg(h.h, strTransferEncoding)
	} else {
		h.contentLengthBytes = h.contentLengthBytes[:0]
		value := strChunked
		if contentLength == -2 {
			h.SetConnectionClose()
			value = strIdentity
		}
		h.h = setArg(h.h, strTransferEncoding, value)
	}
}

// Server returns Server header value.
func (h *ResponseHeader) Server() []byte {
	h.parseRawHeaders()
	return h.server
}

// SetServer sets Server header value.
func (h *ResponseHeader) SetServer(server string) {
	h.parseRawHeaders()
	h.server = AppendBytesStr(h.server[:0], server)
}

// SetServerBytes sets Server header value.
//
// It is safe modifying server buffer after function return.
func (h *ResponseHeader) SetServerBytes(server []byte) {
	h.parseRawHeaders()
	h.server = append(h.server[:0], server...)
}

// ContentType returns Content-Type header value.
func (h *ResponseHeader) ContentType() []byte {
	h.parseRawHeaders()
	contentType := h.contentType
	if len(h.contentType) == 0 {
		contentType = defaultContentType
	}
	return contentType
}

// SetContentType sets Content-Type header value.
func (h *ResponseHeader) SetContentType(contentType string) {
	h.parseRawHeaders()
	h.contentType = AppendBytesStr(h.contentType[:0], contentType)
}

// SetContentTypeBytes sets Content-Type header value.
//
// It is safe modifying contentType buffer after function return.
func (h *ResponseHeader) SetContentTypeBytes(contentType []byte) {
	h.parseRawHeaders()
	h.contentType = append(h.contentType[:0], contentType...)
}

// Len returns the number of headers set,
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
	h.parseRawHeaders()
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
	h.parseRawHeaders()
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.h = delArg(h.h, k)
}

// DelBytes deletes header with the given key.
func (h *ResponseHeader) DelBytes(key []byte) {
	h.parseRawHeaders()
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.h = delArg(h.h, h.bufKV.key)
}

// CopyTo copies all the headers to dst.
func (h *ResponseHeader) CopyTo(dst *ResponseHeader) {
	dst.Clear()
	dst.StatusCode = h.StatusCode
	dst.connectionClose = h.connectionClose
	dst.contentLength = h.contentLength
	dst.contentLengthBytes = append(dst.contentLengthBytes[:0], h.contentLengthBytes...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.server = append(dst.server[:0], h.server...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
	dst.rawHeaders = append(dst.rawHeaders[:0], h.rawHeaders...)
	dst.rawHeadersParsed = h.rawHeadersParsed
}

// VisitAll calls f for each header
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *ResponseHeader) VisitAll(f func(key, value []byte)) {
	h.parseRawHeaders()
	if len(h.contentLengthBytes) > 0 {
		f(strContentLength, h.contentLengthBytes)
	}
	contentType := h.ContentType()
	if len(contentType) > 0 {
		f(strContentType, contentType)
	}
	server := h.Server()
	if len(server) > 0 {
		f(strServer, server)
	}
	if len(h.cookies) > 0 {
		visitArgs(h.cookies, func(k, v []byte) {
			f(strSetCookie, v)
		})
	}
	visitArgs(h.h, f)
	if h.ConnectionClose() {
		f(strConnection, strClose)
	}
}

// Clear clears response header.
func (h *ResponseHeader) Clear() {
	h.StatusCode = 0
	h.connectionClose = false

	h.contentLength = 0
	h.contentLengthBytes = h.contentLengthBytes[:0]

	h.server = h.server[:0]
	h.contentType = h.contentType[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]

	h.rawHeaders = h.rawHeaders[:0]
	h.rawHeadersParsed = false
}

// VisitAllCookie calls f for each response cookie.
//
// Cookie name is passed in key and the whole Set-Cookie header value
// is passed in value on each f invocation. Value may be parsed
// with Cookie.ParseBytes().
//
// f must not retain references to key and/or value after returning.
func (h *ResponseHeader) VisitAllCookie(f func(key, value []byte)) {
	h.parseRawHeaders()
	visitArgs(h.cookies, f)
}

// Write writs response header to w.
func (h *ResponseHeader) Write(w *bufio.Writer) error {
	h.parseRawHeaders()
	statusCode := h.StatusCode
	if statusCode < 0 {
		return fmt.Errorf("response cannot have negative status code=%d", statusCode)
	}
	if statusCode == 0 {
		statusCode = StatusOK
	}
	w.Write(statusLine(statusCode))

	server := h.Server()
	if len(server) == 0 {
		server = defaultServerName
	}
	writeHeaderLine(w, strServer, server)
	writeHeaderLine(w, strDate, serverDate.Load().([]byte))

	contentType := h.ContentType()
	writeHeaderLine(w, strContentType, contentType)

	if len(h.contentLengthBytes) > 0 {
		writeHeaderLine(w, strContentLength, h.contentLengthBytes)
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

	if h.ConnectionClose() {
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
		// treat all errors on the first byte read as EOF
		if n == 1 || err == io.EOF {
			return io.EOF
		}
		return fmt.Errorf("error when reading response headers: %s", err)
	}
	isEOF := (err != nil)
	b = mustPeekBuffered(r)
	var headersLen int
	if headersLen, err = h.parse(b); err != nil {
		if err == errNeedMore && !isEOF {
			return err
		}
		return fmt.Errorf("erorr when reading response headers: %s", err)
	}
	mustDiscard(r, headersLen)
	return nil
}

func (h *ResponseHeader) parse(buf []byte) (int, error) {
	m, err := h.parseFirstLine(buf)
	if err != nil {
		return 0, err
	}
	rawHeaders, n, err := readRawHeaders(h.rawHeaders, buf[m:])
	if err != nil {
		return 0, err
	}
	h.rawHeaders = rawHeaders
	return m + n, nil
}

func (h *ResponseHeader) parseFirstLine(buf []byte) (int, error) {
	bNext := buf
	var b []byte
	var err error
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return 0, err
		}
	}

	// parse protocol
	n := bytes.IndexByte(b, ' ')
	if n < 0 {
		return 0, fmt.Errorf("cannot find whitespace in the first line of response %q", buf)
	}
	if !bytes.Equal(b[:n], strHTTP11) {
		// Non-http/1.1 response. Close connection after it.
		h.connectionClose = true
	}
	b = b[n+1:]

	// parse status code
	h.StatusCode, n, err = parseUintBuf(b)
	if err != nil {
		return 0, fmt.Errorf("cannot parse response status code: %s. Response %q", err, buf)
	}
	if len(b) > n && b[n] != ' ' {
		return 0, fmt.Errorf("unexpected char at the end of status code. Response %q", buf)
	}

	return len(buf) - len(bNext), nil
}

func (h *ResponseHeader) parseRawHeaders() {
	if h.rawHeadersParsed {
		return
	}
	h.rawHeadersParsed = true
	if len(h.rawHeaders) == 0 {
		return
	}

	// 'identity' content-length by default
	h.contentLength = -2

	var s headerScanner
	s.init(h.rawHeaders)
	var err error
	var kv *argsKV
	for s.next() {
		switch {
		case bytes.Equal(s.key, strContentType):
			h.contentType = append(h.contentType[:0], s.value...)
		case bytes.Equal(s.key, strServer):
			h.server = append(h.server[:0], s.value...)
		case bytes.Equal(s.key, strContentLength):
			if h.contentLength != -1 {
				if h.contentLength, err = parseContentLength(s.value); err != nil {
					h.contentLength = -2
				} else {
					h.contentLengthBytes = append(h.contentLengthBytes[:0], s.value...)
				}
			}
		case bytes.Equal(s.key, strTransferEncoding):
			if !bytes.Equal(s.value, strIdentity) {
				h.contentLength = -1
				h.h = setArg(h.h, strTransferEncoding, strChunked)
			}
		case bytes.Equal(s.key, strConnection):
			if bytes.Equal(s.value, strClose) {
				h.connectionClose = true
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

	if h.contentLength < 0 {
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}
	if h.contentLength == -2 {
		h.h = setArg(h.h, strTransferEncoding, strIdentity)
		h.connectionClose = true
	}
}

// ConnectionClose returns true if 'Connection: close' header is set.
func (h *RequestHeader) ConnectionClose() bool {
	// h.parseRawHeaders() isn't called for performance reasons.
	if !h.rawHeadersParsed {
		if h.connectionClose {
			return true
		}
		if hasRawHeader(h.rawHeaders, strConnectionClose) {
			h.connectionClose = true
			return true
		}
	}
	return h.connectionClose
}

// SetConnectionClose sets 'Connection: close' header.
func (h *RequestHeader) SetConnectionClose() {
	// h.parseRawHeaders() isn't called for performance reasons.
	h.connectionClose = true
}

// ContentLength returns Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
func (h *RequestHeader) ContentLength() int {
	if !h.IsPost() {
		return 0
	}
	h.parseRawHeaders()
	return h.contentLength
}

// SetContentLength sets Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
func (h *RequestHeader) SetContentLength(contentLength int) {
	h.parseRawHeaders()
	h.contentLength = contentLength
	if contentLength >= 0 {
		h.contentLengthBytes = AppendUint(h.contentLengthBytes[:0], contentLength)
		h.h = delArg(h.h, strTransferEncoding)
	} else {
		h.contentLengthBytes = h.contentLengthBytes[:0]
		h.h = setArg(h.h, strTransferEncoding, strChunked)
	}
}

// ContentType returns Content-Type header value.
func (h *RequestHeader) ContentType() []byte {
	h.parseRawHeaders()
	return h.contentType
}

// SetContentType sets Content-Type header value.
func (h *RequestHeader) SetContentType(contentType string) {
	h.parseRawHeaders()
	h.contentType = AppendBytesStr(h.contentType[:0], contentType)
}

// SetContentTypeBytes sets Content-Type header value.
//
// It is safe modifying contentType buffer after function return.
func (h *RequestHeader) SetContentTypeBytes(contentType []byte) {
	h.parseRawHeaders()
	h.contentType = append(h.contentType[:0], contentType...)
}

// Host returns Host header value.
func (h *RequestHeader) Host() []byte {
	h.parseRawHeaders()
	return h.host
}

// SetHost sets Host header value.
func (h *RequestHeader) SetHost(host string) {
	h.parseRawHeaders()
	h.host = AppendBytesStr(h.host[:0], host)
}

// SetHostBytes sets Host header value.
//
// It is safe modifying host buffer after function return.
func (h *RequestHeader) SetHostBytes(host []byte) {
	h.parseRawHeaders()
	h.host = append(h.host[:0], host...)
}

// UserAgent returns User-Agent header value.
func (h *RequestHeader) UserAgent() []byte {
	h.parseRawHeaders()
	return h.userAgent
}

// SetUserAgent sets User-Agent header value.
func (h *RequestHeader) SetUserAgent(userAgent string) {
	h.parseRawHeaders()
	h.userAgent = AppendBytesStr(h.userAgent[:0], userAgent)
}

// SetUserAgentBytes sets User-Agent header value.
//
// It is safe modifying userAgent buffer after function return.
func (h *RequestHeader) SetUserAgentBytes(userAgent []byte) {
	h.parseRawHeaders()
	h.userAgent = append(h.userAgent[:0], userAgent...)
}

// Len returns the number of headers set
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
	h.parseRawHeaders()
	switch {
	case bytes.Equal(strContentType, key):
		h.SetContentTypeBytes(value)
	case bytes.Equal(strServer, key):
		h.SetServerBytes(value)
	case bytes.Equal(strSetCookie, key):
		var kv *argsKV
		h.cookies, kv = allocArg(h.cookies)
		kv.key = getCookieKey(kv.key, value)
		kv.value = append(kv.value[:0], value...)
	case bytes.Equal(strContentLength, key):
		if contentLength, err := parseContentLength(value); err == nil {
			h.contentLength = contentLength
			h.contentLengthBytes = append(h.contentLengthBytes[:0], value...)
		}
	case bytes.Equal(strConnection, key):
		if bytes.Equal(strClose, value) {
			h.SetConnectionClose()
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

// GetCookie fills cookie for the given cookie.Key.
//
// Returns false if cookie with the given cookie.Key is missing
func (h *ResponseHeader) GetCookie(cookie *Cookie) bool {
	h.parseRawHeaders()
	v := peekArgBytes(h.cookies, cookie.Key)
	if v == nil {
		return false
	}
	cookie.ParseBytes(v)
	return true
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
	h.parseRawHeaders()
	switch {
	case bytes.Equal(strContentType, key):
		return h.ContentType()
	case bytes.Equal(strServer, key):
		return h.Server()
	case bytes.Equal(strConnection, key):
		if h.ConnectionClose() {
			return strClose
		}
		return nil
	case bytes.Equal(strContentLength, key):
		return h.contentLengthBytes
	default:
		return peekArgBytes(h.h, key)
	}
}

// RequestHeader represents HTTP request header.
//
// It is forbidden copying RequestHeader instances.
// Create new instances instead and use CopyTo.
type RequestHeader struct {
	connectionClose bool

	contentLength      int
	contentLengthBytes []byte

	method      []byte
	requestURI  []byte
	host        []byte
	contentType []byte
	userAgent   []byte

	h     []argsKV
	bufKV argsKV

	cookies          []argsKV
	cookiesCollected bool

	rawHeaders       []byte
	rawHeadersParsed bool
}

// Del deletes header with the given key.
func (h *RequestHeader) Del(key string) {
	h.parseRawHeaders()
	k := getHeaderKeyBytes(&h.bufKV, key)
	h.h = delArg(h.h, k)
}

// DelBytes deletes header with the given key.
func (h *RequestHeader) DelBytes(key []byte) {
	h.parseRawHeaders()
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key)
	h.h = delArg(h.h, h.bufKV.key)
}

// VisitAllCookie calls f for each request cookie.
//
// f must not retain references to key and/or value after returning.
func (h *RequestHeader) VisitAllCookie(f func(key, value []byte)) {
	h.parseRawHeaders()
	h.collectCookies()
	visitArgs(h.cookies, f)
}

// VisitAll calls f for each header
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *RequestHeader) VisitAll(f func(key, value []byte)) {
	h.parseRawHeaders()
	host := h.Host()
	if len(host) > 0 {
		f(strHost, host)
	}
	if len(h.contentLengthBytes) > 0 {
		f(strContentLength, h.contentLengthBytes)
	}
	contentType := h.ContentType()
	if len(contentType) > 0 {
		f(strContentType, contentType)
	}
	userAgent := h.UserAgent()
	if len(userAgent) > 0 {
		f(strUserAgent, userAgent)
	}

	h.collectCookies()
	if len(h.cookies) > 0 {
		h.bufKV.value = appendRequestCookieBytes(h.bufKV.value[:0], h.cookies)
		f(strCookie, h.bufKV.value)
	}
	visitArgs(h.h, f)
	if h.ConnectionClose() {
		f(strConnection, strClose)
	}
}

// CopyTo copies all the headers to dst.
func (h *RequestHeader) CopyTo(dst *RequestHeader) {
	dst.Clear()
	dst.connectionClose = h.connectionClose
	dst.contentLength = h.contentLength
	dst.contentLengthBytes = append(dst.contentLengthBytes[:0], h.contentLengthBytes...)
	dst.method = append(dst.method[:0], h.host...)
	dst.requestURI = append(dst.requestURI[:0], h.requestURI...)
	dst.host = append(dst.host[:0], h.host...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.userAgent = append(dst.userAgent[:0], h.userAgent...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
	dst.cookiesCollected = h.cookiesCollected
	dst.rawHeaders = append(dst.rawHeaders[:0], h.rawHeaders...)
	dst.rawHeadersParsed = h.rawHeadersParsed
}

// RequestURI returns RequestURI from the first HTTP request line.
func (h *RequestHeader) RequestURI() []byte {
	requestURI := h.requestURI
	if len(requestURI) == 0 {
		requestURI = strSlash
	}
	return requestURI
}

// SetRequestURI sets RequestURI for the first HTTP request line.
// RequestURI must be properly encoded.
// Use URI.RequestURI for constructing proper RequestURI if unsure.
func (h *RequestHeader) SetRequestURI(requestURI string) {
	h.requestURI = AppendBytesStr(h.requestURI, requestURI)
}

// SetRequestURI sets RequestURI for the first HTTP request ine.
// RequestURI must be properly encoded.
// Use URI.RequestURI for constructing proper RequestURI if unsure.
//
// It is safe modifying requestURI buffer after function return.
func (h *RequestHeader) SetRequestURIBytes(requestURI []byte) {
	h.requestURI = append(h.requestURI[:0], requestURI...)
}

// Method returns HTTP request method.
func (h *RequestHeader) Method() []byte {
	if len(h.method) == 0 {
		return strGet
	}
	return h.method
}

// SetMethod sets HTTP request method
func (h *RequestHeader) SetMethod(method string) {
	h.method = AppendBytesStr(h.method, method)
}

// SetMethod sets HTTP request method.
//
// It is safe medifying method buffer after function return.
func (h *RequestHeader) SetMethodBytes(method []byte) {
	h.method = append(h.method[:0], method...)
}

// IsGet returns true if request method is GET.
func (h *RequestHeader) IsGet() bool {
	return bytes.Equal(h.Method(), strGet)
}

// IsPost returns true if request method is POST.
func (h *RequestHeader) IsPost() bool {
	return bytes.Equal(h.Method(), strPost)
}

// IsHead returns true if request method is HEAD.
func (h *RequestHeader) IsHead() bool {
	return bytes.Equal(h.Method(), strHead)
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
	h.parseRawHeaders()
	h.collectCookies()
	h.cookies = setArg(h.cookies, key, value)
}

// Clear clears request header
func (h *RequestHeader) Clear() {
	h.connectionClose = false

	h.contentLength = 0
	h.contentLengthBytes = h.contentLengthBytes[:0]

	h.method = h.method[:0]
	h.requestURI = h.requestURI[:0]
	h.host = h.host[:0]
	h.contentType = h.contentType[:0]
	h.userAgent = h.userAgent[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]
	h.cookiesCollected = false

	h.rawHeaders = h.rawHeaders[:0]
	h.rawHeadersParsed = false
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
	h.parseRawHeaders()
	switch {
	case bytes.Equal(strHost, key):
		h.SetHostBytes(value)
	case bytes.Equal(strContentType, key):
		h.SetContentTypeBytes(value)
	case bytes.Equal(strUserAgent, key):
		h.SetUserAgentBytes(value)
	case bytes.Equal(strCookie, key):
		h.collectCookies()
		h.cookies = parseRequestCookies(h.cookies, value)
	case bytes.Equal(strContentLength, key):
		if contentLength, err := parseContentLength(value); err == nil {
			h.contentLength = contentLength
			h.contentLengthBytes = append(h.contentLengthBytes[:0], value...)
		}
	case bytes.Equal(strConnection, key):
		if bytes.Equal(strClose, value) {
			h.SetConnectionClose()
		}
		// skip other 'Connection' shit :)
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
	h.parseRawHeaders()
	h.collectCookies()
	return peekArgStr(h.cookies, key)
}

// PeekCookieBytes returns cookie for the given key
func (h *RequestHeader) PeekCookieBytes(key []byte) []byte {
	h.parseRawHeaders()
	h.collectCookies()
	return peekArgBytes(h.cookies, key)
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
	h.parseRawHeaders()
	switch {
	case bytes.Equal(strHost, key):
		return h.Host()
	case bytes.Equal(strContentType, key):
		return h.ContentType()
	case bytes.Equal(strUserAgent, key):
		return h.UserAgent()
	case bytes.Equal(strConnection, key):
		if h.ConnectionClose() {
			return strClose
		}
		return nil
	case bytes.Equal(strContentLength, key):
		return h.contentLengthBytes
	default:
		return peekArgBytes(h.h, key)
	}
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
		// treat all errors on the first byte read as EOF
		if n == 1 || err == io.EOF {
			return io.EOF
		}
		return fmt.Errorf("error when reading request headers: %s", err)
	}
	isEOF := (err != nil)
	b = mustPeekBuffered(r)
	var headersLen int
	if headersLen, err = h.parse(b); err != nil {
		if err == errNeedMore && !isEOF {
			return err
		}
		return fmt.Errorf("error when reading request headers: %s", err)
	}
	mustDiscard(r, headersLen)
	return nil
}

func (h *RequestHeader) parse(buf []byte) (int, error) {
	m, err := h.parseFirstLine(buf)
	if err != nil {
		return 0, err
	}
	rawHeaders, n, err := readRawHeaders(h.rawHeaders, buf[m:])
	if err != nil {
		return 0, err
	}
	h.rawHeaders = rawHeaders
	return m + n, nil
}

func (h *RequestHeader) parseFirstLine(buf []byte) (int, error) {
	bNext := buf
	var b []byte
	var err error
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return 0, err
		}
	}

	// parse method
	n := bytes.IndexByte(b, ' ')
	if n <= 0 {
		return 0, fmt.Errorf("cannot find http request method in %q", buf)
	}
	h.method = append(h.method[:0], b[:n]...)
	b = b[n+1:]

	// parse requestURI
	n = bytes.LastIndexByte(b, ' ')
	if n < 0 {
		// no http protocol found. Close connection after the request.
		h.connectionClose = true
		n = len(b)
	} else if n == 0 {
		return 0, fmt.Errorf("RequestURI cannot be empty in %q", buf)
	} else if !bytes.Equal(b[n+1:], strHTTP11) {
		// non-http/1.1 protocol. Close connection after the request.
		h.connectionClose = true
	}
	h.requestURI = append(h.requestURI[:0], b[:n]...)

	return len(buf) - len(bNext), nil
}

func readRawHeaders(dst, buf []byte) ([]byte, int, error) {
	dst = dst[:0]
	n := bytes.IndexByte(buf, '\n')
	if n < 0 {
		return nil, 0, errNeedMore
	}
	if (n == 1 && buf[0] == '\r') || n == 0 {
		// empty headers
		return dst, n + 1, nil
	}

	n++
	b := buf
	m := n
	for {
		b = b[m:]
		m = bytes.IndexByte(b, '\n')
		if m < 0 {
			return nil, 0, errNeedMore
		}
		m++
		n += m
		if (m == 2 && b[0] == '\r') || m == 1 {
			dst = append(dst, buf[:n]...)
			return dst, n, nil
		}
	}
}

func (h *RequestHeader) parseRawHeaders() {
	if h.rawHeadersParsed {
		return
	}
	h.rawHeadersParsed = true
	if len(h.rawHeaders) == 0 {
		return
	}

	h.contentLength = -2

	var s headerScanner
	s.init(h.rawHeaders)
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
			if h.contentLength != -1 {
				if h.contentLength, err = parseContentLength(s.value); err != nil {
					h.contentLength = -2
				} else {
					h.contentLengthBytes = append(h.contentLengthBytes[:0], s.value...)
				}
			}
		case bytes.Equal(s.key, strTransferEncoding):
			if !bytes.Equal(s.value, strIdentity) {
				h.contentLength = -1
				h.h = setArg(h.h, strTransferEncoding, strChunked)
			}
		case bytes.Equal(s.key, strConnection):
			if bytes.Equal(s.key, strConnection) {
				h.connectionClose = true
			}
		default:
			h.h, kv = allocArg(h.h)
			kv.key = append(kv.key[:0], s.key...)
			kv.value = append(kv.value[:0], s.value...)
		}
	}

	if h.contentLength < 0 {
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}

	if !h.IsPost() {
		h.contentLength = 0
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}
}

func (h *RequestHeader) collectCookies() {
	if h.cookiesCollected {
		return
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		if bytes.Equal(kv.key, strCookie) {
			h.cookies = parseRequestCookies(h.cookies, kv.value)
			tmp := *kv
			copy(h.h[i:], h.h[i+1:])
			n--
			i--
			h.h[n] = tmp
			h.h = h.h[:n]
		}
	}
	h.cookiesCollected = true
}

// Write writes request header to w.
func (h *RequestHeader) Write(w *bufio.Writer) error {
	h.parseRawHeaders()
	method := h.Method()
	w.Write(method)
	w.WriteByte(' ')

	requestURI := h.RequestURI()
	w.Write(requestURI)
	w.WriteByte(' ')
	w.Write(strHTTP11)
	w.Write(strCRLF)

	userAgent := h.UserAgent()
	if len(userAgent) == 0 {
		userAgent = defaultUserAgent
	}
	writeHeaderLine(w, strUserAgent, userAgent)

	host := h.Host()
	if len(host) == 0 {
		return fmt.Errorf("missing required Host header")
	}
	writeHeaderLine(w, strHost, host)

	if h.IsPost() {
		contentType := h.ContentType()
		if len(contentType) == 0 {
			return fmt.Errorf("missing required Content-Type header for POST request")
		}
		writeHeaderLine(w, strContentType, contentType)

		if len(h.contentLengthBytes) > 0 {
			writeHeaderLine(w, strContentLength, h.contentLengthBytes)
		}
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		writeHeaderLine(w, kv.key, kv.value)
	}

	h.collectCookies()
	n := len(h.cookies)
	if n > 0 {
		h.bufKV.value = appendRequestCookieBytes(h.bufKV.value[:0], h.cookies)
		writeHeaderLine(w, strCookie, h.bufKV.value)
	}

	if h.ConnectionClose() {
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
	b     []byte
	key   []byte
	value []byte
	err   error
}

func (s *headerScanner) init(headers []byte) {
	s.b = headers
	s.key = nil
	s.value = nil
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

	n := bytes.IndexByte(b, ':')
	if n < 0 {
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

func hasRawHeader(buf, s []byte) bool {
	n := bytes.Index(buf, s)
	if n < 0 {
		return false
	}
	if n > 0 && buf[n-1] != '\n' {
		return false
	}
	n += len(s)
	if n >= len(buf) {
		return false
	}
	switch buf[n] {
	case '\r':
		return len(buf) > n+1 && buf[n+1] == '\n'
	case '\n':
		return true
	default:
		return false
	}
}
