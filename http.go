package fasthttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Request represents HTTP request.
//
// It is forbidden copying Request instances. Create new instances
// and use CopyTo() instead.
type Request struct {
	// Request header
	Header RequestHeader

	// Request body.
	Body []byte

	// Request URI.
	// URI becomes avaiable only after Request.ParseURI() call.
	URI       URI
	parsedURI bool

	// Arguments sent in POST.
	// PostArgs becomes available only after Request.ParesPostArgs() call.
	PostArgs       Args
	parsedPostArgs bool
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
		req.Header.ContentLength = len(req.Body)
	}
	return nil
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
		panic("BUG: expected zero-length buffer")
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

	// Response body.
	//
	// Either Body or or BodyStream may be set, but not both.
	Body []byte

	// Response body stream.
	//
	// BodyStream may be set instead of Body for performance reasons only.
	//
	// BodyStream is read by Response.Write() until one of the following
	// evnets occur:
	// - Response.ContentLength bytes read if it is greater than 0.
	// - error or io.EOF is returned from BodyStream if response.ContentLength
	// is 0 or negative
	//
	// Response.Write() calls BodyStream.Close() (io.Closer) if such method
	// is present after finishing reading BodyStream.
	//
	// Either BodyStream or Body may be set, but not both
	//
	// Response.Read() never sets BodySteam -- it sets only Body.
	BodyStream io.Reader

	// if set to true, Response.Read() skips reading body.
	// Use it for HEAD requests
	SkipBody bool
}

// CopyTo copies resp contents to dst except of BodyStream.
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
	resp.BodyStream = nil
}

// Read reads response (including body) from the given r.
func (resp *Response) Read(r *bufio.Reader) error {
	resp.clearSkipHeader()
	err := resp.Header.Read(r)
	if err != nil {
		return err
	}

	if !isSkipResponseBody(resp.Header.StatusCode) && !resp.SkipBody {
		body, err := readBody(r, resp.Header.ContentLength, resp.Body)
		if err != nil {
			resp.Clear()
			return err
		}
		resp.Body = body
		resp.Header.ContentLength = len(resp.Body)
	}
	return nil
}

// Write writes response to w.
//
// Write doesn't flush request to w for performance reasons.
func (resp *Response) Write(w *bufio.Writer) error {
	var err error
	if resp.BodyStream != nil {
		if resp.Header.ContentLength > 0 {
			if err = resp.Header.Write(w); err != nil {
				return err
			}
			if err = writeBodyFixedSize(w, resp.BodyStream, resp.Header.ContentLength); err != nil {
				return err
			}
		} else {
			resp.Header.ContentLength = -1
			if err = resp.Header.Write(w); err != nil {
				return err
			}
			if err = writeBodyChunked(w, resp.BodyStream); err != nil {
				return err
			}
		}
		if bsc, ok := resp.BodyStream.(io.Closer); ok {
			err = bsc.Close()
		}
		return err
	}

	resp.Header.ContentLength = len(resp.Body)
	if err = resp.Header.Write(w); err != nil {
		return err
	}
	_, err = w.Write(resp.Body)
	return err
}

func writeBodyChunked(w *bufio.Writer, r io.Reader) error {
	vbuf := copyBufPool.Get()
	if vbuf == nil {
		vbuf = make([]byte, 4096)
	}
	buf := vbuf.([]byte)

	var err error
	var n int
	for {
		n, err = r.Read(buf)
		if n == 0 {
			if err == nil {
				panic("BUG: io.Reader returned 0, nil")
			}
			if err == io.EOF {
				if err = writeChunk(w, buf[:0]); err != nil {
					break
				}
				err = nil
			}
			break
		}
		if err = writeChunk(w, buf[:n]); err != nil {
			break
		}
	}

	copyBufPool.Put(vbuf)
	return err
}

func writeChunk(w *bufio.Writer, b []byte) error {
	n := len(b)
	writeHexInt(w, n)
	w.Write(strCRLF)
	w.Write(b)
	_, err := w.Write(strCRLF)
	return err
}

var copyBufPool sync.Pool

func isSkipResponseBody(statusCode int) bool {
	// From http/1.1 specs:
	// All 1xx (informational), 204 (no content), and 304 (not modified) responses MUST NOT include a message-body
	if statusCode >= 100 && statusCode < 200 {
		return true
	}
	return statusCode == StatusNoContent || statusCode == StatusNotModified
}

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

var limitReaderPool sync.Pool

func writeBodyFixedSize(w *bufio.Writer, r io.Reader, size int) error {
	vbuf := copyBufPool.Get()
	if vbuf == nil {
		vbuf = make([]byte, 4096)
	}
	buf := vbuf.([]byte)

	vlr := limitReaderPool.Get()
	if vlr == nil {
		vlr = &io.LimitedReader{}
	}
	lr := vlr.(*io.LimitedReader)
	lr.R = r
	lr.N = int64(size)

	n, err := io.CopyBuffer(w, lr, buf)

	limitReaderPool.Put(vlr)
	copyBufPool.Put(vbuf)

	if n != int64(size) && err == nil {
		err = fmt.Errorf("read %d bytes from BodyStream instead of %d bytes", n, size)
	}
	return err
}
