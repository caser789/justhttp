package fasthttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
)

// Request represents HTTP request.
//
// It is forbidden copying Request instances. Create new instances
// and use CopyTo() instead.
type Request struct {
	// Request header
	Header RequestHeader

	// Request body
	Body []byte

	// Request URI.
	// URI becomes avaiable only after Request.ParseURI() call.
	URI       URI
	parsedURI bool

	// Arguments sent in POST.
	// PostArgs becomes available only after Request.ParesPostArgs() call.
	PostArgs       Args
	parsedPostArgs bool

	timeoutCh    chan error
	timeoutTimer *time.Timer
}

// CopyTo copies req contents to dst
func (req *Request) CopyTo(dst *Request) {
	dst.Clear()
	req.Header.CopyTo(&dst.Header)
	dst.Body = append(dst.Body[:0], req.Body...)
	if req.parsedURI {
		dst.ParseURI()
	}
	if req.parsedPostArgs {
		dst.ParsePostArgs()
	}
}

// ParseURI parses request uri and fills Request.URI.
func (req *Request) ParseURI() {
	if req.parsedURI {
		return
	}
	req.URI.Parse(req.Header.host, req.Header.RequestURI)
	req.parsedURI = true
}

// ParsePostArgs parses args sent in POST body and fills Request.PostArgs
func (req *Request) ParsePostArgs() error {
	if req.parsedPostArgs {
		return nil
	}

	if !req.Header.IsMethodPost() {
		return fmt.Errorf("Cannot parse POST args for %q request", req.Header.Method)
	}
	if !bytes.Equal(req.Header.contentType, strPostArgsContentType) {
		return fmt.Errorf("Cannot parse POST args for %q Content-Type. Required %q Content-Type",
			req.Header.contentType, strPostArgsContentType)
	}
	req.PostArgs.ParseBytes(req.Body)
	req.parsedPostArgs = true
	return nil
}

// Clear clears request contents.
func (req *Request) Clear() {
	req.Header.Clear()
	req.clearSkipHeader()
}

func (req *Request) clearSkipHeader() {
	req.Body = req.Body[:0]
	req.URI.Clear()
	req.parsedURI = false
	req.PostArgs.Clear()
	req.parsedPostArgs = false
}

// Read reads request (including body) from the given r.
func (req *Request) Read(r *bufio.Reader) error {
	req.clearSkipHeader()
	err := req.Header.Read(r)
	if err != nil {
		return err
	}

	if req.Header.IsMethodPost() {
		body, err := readBody(r, req.Header.ContentLength, req.Body)
		if err != nil {
			req.Clear()
			return err
		}
		req.Body = body
	}
	return nil
}

// ReadTimeout reads request (including body) from the given r during
// the given timeout.
//
// If request couldnt' be read during the given timeout,
// ErrReadTimeout is returned
// Request can no longer be used after ErrReadTimeout error.
func (req *Request) ReadTimeout(r *bufio.Reader, timeout time.Duration) error {
	if timeout <= 0 {
		return req.Read(r)
	}

	ch := req.timeoutCh
	if ch == nil {
		ch = make(chan error, 1)
		req.timeoutCh = ch
	} else if len(ch) > 0 {
		panic("BUG: Request.timeoutCh must be empty!")
	}

	go func() {
		ch <- req.Read(r)
	}()

	var err error
	req.timeoutTimer = initTimer(req.timeoutTimer, timeout)
	select {
	case err = <-ch:
	case <-req.timeoutTimer.C:
		req.timeoutCh = nil
		err = ErrReadTimeout
	}
	stopTimer(req.timeoutTimer)
	return err
}

// Write writes request to w.
//
// Write doesn't flush request to w for performance reasons.
func (req *Request) Write(w *bufio.Writer) error {
	contentLengthOld := req.Header.ContentLength
	req.Header.ContentLength = len(req.Body)
	err := req.Header.Write(w)
	req.Header.ContentLength = contentLengthOld
	if err != nil {
		return err
	}
	if req.Header.IsMethodPost() {
		_, err = w.Write(req.Body)
	} else if len(req.Body) > 0 {
		return fmt.Errorf("Non-zero body of non-POST request. body=%q", req.Body)
	}
	return err
}

func readBody(r *bufio.Reader, contentLength int, b []byte) ([]byte, error) {
	b = b[:0]
	if contentLength >= 0 {
		return readBodyFixedSize(r, contentLength, b)
	}
	return readBodyChunked(r, b)
}

func readBodyFixedSize(r *bufio.Reader, n int, buf []byte) ([]byte, error) {
	if n == 0 {
		return buf, nil
	}

	bufLen := len(buf)
	bufCap := bufLen + n
	if cap(buf) < bufCap {
		b := make([]byte, bufLen, round2(bufCap))
		copy(b, buf)
		buf = b
	}
	buf = buf[:bufCap]
	b := buf[bufLen:]

	for {
		nn, err := r.Read(b)
		if nn <= 0 {
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return nil, err
			}
			panic(fmt.Sprintf("BUF: bufio.Read() returned (%d, nil)", nn))
		}
		if nn == n {
			return buf, nil
		}
		if nn > n {
			panic(fmt.Sprintf("BUF: read more than requested: %d vs %d", nn, n))
		}
		n -= nn
		b = b[nn:]
	}
}

func readBodyChunked(r *bufio.Reader, b []byte) ([]byte, error) {
	if len(b) > 0 {
		panic("Expected zero-length buffer")
	}

	strCRLFLen := len(strCRLF)
	for {
		chunkSize, err := parseChunkSize(r)
		if err != nil {
			return nil, err
		}
		b, err = readBodyFixedSize(r, chunkSize+strCRLFLen, b)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(b[len(b)-strCRLFLen:], strCRLF) {
			return nil, fmt.Errorf("cannot find crlf at the end of chunk")
		}
		b = b[:len(b)-strCRLFLen]
		if chunkSize == 0 {
			return b, nil
		}
	}
}

func parseChunkSize(r *bufio.Reader) (int, error) {
	n, err := readHexInt(r)
	if err != nil {
		return -1, err
	}
	c, err := r.ReadByte()
	if err != nil {
		return -1, fmt.Errorf("cannot read '\r' char at the end of chunk size: %s", err)
	}
	if c != '\r' {
		return -1, fmt.Errorf("unexpected char %q at the end of chunk size. Expected %q", c, '\r')
	}
	c, err = r.ReadByte()
	if err != nil {
		return -1, fmt.Errorf("cannot read '\n' char at the end of chunk size: %s", err)
	}
	if c != '\n' {
		return -1, fmt.Errorf("unexpected char %q at the end of chunk size. Expected %q", c, '\n')
	}
	return n, nil
}

// Response represents HTTP response
//
// It is forbidden copying Response instances. Create new instances
// and use CopyTo() instead.
type Response struct {
	// Response header
	Header ResponseHeader

	// Resposne body
	Body []byte

	// if set to true, Response.Read() skips reading body.
	// Use it for HEAD requests
	SkipBody bool

	timeoutCh    chan error
	timeoutTimer *time.Timer
}

// CopyTo copies resp contents to dst.
func (resp *Response) CopyTo(dst *Response) {
	dst.Clear()
	resp.Header.CopyTo(&dst.Header)
	dst.Body = append(dst.Body[:0], resp.Body...)
	dst.SkipBody = resp.SkipBody
}

// Clear clears response contents.
func (resp *Response) Clear() {
	resp.Header.Clear()
	resp.clearSkipHeader()
}

func (resp *Response) clearSkipHeader() {
	resp.Body = resp.Body[:0]
}

// Read reads response (including body) from the given r.
func (resp *Response) Read(r *bufio.Reader) error {
	resp.clearSkipHeader()
	err := resp.Header.Read(r)
	if err != nil {
		return err
	}

	if isSkipResponseBody(resp.Header.StatusCode) || resp.SkipBody {
		resp.SkipBody = false
		return nil
	}

	body, err := readBody(r, resp.Header.ContentLength, resp.Body)
	if err != nil {
		resp.Clear()
		return err
	}
	resp.Body = body
	return nil
}

// ReadTimeout reads response (including body) from the given r during
// the given timeout.
//
// If response coundn't be read during the given timeout,
// ErrReadTimeout is returned.
// Response can no longer be used after ErrReadTimeout error.
func (resp *Response) ReadTimeout(r *bufio.Reader, timeout time.Duration) error {
	if timeout <= 0 {
		return resp.Read(r)
	}

	ch := resp.timeoutCh
	if ch == nil {
		ch = make(chan error, 1)
		resp.timeoutCh = ch
	} else if len(ch) > 0 {
		panic("BUG: Response.timeoutCh must be empty!")
	}

	go func() {
		ch <- resp.Read(r)
	}()

	var err error
	resp.timeoutTimer = initTimer(resp.timeoutTimer, timeout)
	select {
	case err = <-ch:
	case <-resp.timeoutTimer.C:
		resp.timeoutCh = nil
		err = ErrReadTimeout
	}
	stopTimer(resp.timeoutTimer)
	return err
}

// Write writes response to w.
//
// Write doesn't flush request to w for performance reasons.
func (resp *Response) Write(w *bufio.Writer) error {
	contentLengthOld := resp.Header.ContentLength
	resp.Header.ContentLength = len(resp.Body)
	err := resp.Header.Write(w)
	resp.Header.ContentLength = contentLengthOld
	if err != nil {
		return err
	}
	_, err = w.Write(resp.Body)
	return err
}

func isSkipResponseBody(statusCode int) bool {
	// From http/1.1 specs:
	// All 1xx (informational), 204 (no content), and 304 (not modified) responses MUST NOT include a message-body
	if statusCode >= 100 && statusCode < 200 {
		return true
	}
	return statusCode == StatusNoContent || statusCode == StatusNotModified
}

// ErrReadTimeout may be returned by Request.ReadTimeout
// or Response.ReadTimeout on timeout.
var ErrReadTimeout = errors.New("read timeout")

func round2(n int) int {
	if n <= 0 {
		return 0
	}
	n--
	x := uint(0)
	for n > 0 {
		n >>= 1
		x++
	}
	return 1 << x
}
