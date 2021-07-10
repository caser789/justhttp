package fasthttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type readDeadliner interface {
	SetReadDeadline(time.Time) error
}

type writeDeadliner interface {
	SetWriteDeadline(time.Time) error
}

// Logger is used for logging formatted messages.
type Logger interface {
	// Printf must have the same sematics as log.Printf.
	Printf(format string, args ...interface{})
}

type remoteAddrer interface {
	RemoteAddr() net.Addr
}

// RequestHandler must process incoming requests.
//
// ResponseHandler must call ctx.TimeoutError() before return
// if it keeps references to ctx and/or its members after the return
type RequestHandler func(ctx *RequestCtx)

// TimeoutHandler creates RequestHandler, which returns StatusRequestTimeout
// error with the given msg in the body to the client if h didn't return
// during the given duration.
func TimeoutHandler(h RequestHandler, timeout time.Duration, msg string) RequestHandler {
	if timeout <= 0 {
		return h
	}

	return func(ctx *RequestCtx) {
		ch := ctx.timeoutCh
		if ch == nil {
			ch = make(chan struct{}, 1)
			ctx.timeoutCh = ch
		}
		go func() {
			h(ctx)
			ch <- struct{}{}
		}()
		ctx.timeoutTimer = initTimer(ctx.timeoutTimer, timeout)
		select {
		case <-ch:
		case <-ctx.timeoutTimer.C:
			ctx.TimeoutError(msg)
		}
		stopTimer(ctx.timeoutTimer)
	}
}

// Server implemnts HTTP server.
//
// It is forbidden copying Server instances. Create new Server instances
// instead.
type Server struct {
	// Hanlder for processing incoming requests.
	Handler RequestHandler

	// Server name for sending in response headers.
	//
	// Default server name is used if left blank
	Name string

	// Per-connection buffer size for requests' reading.
	// This also limits the maximum header size.
	//
	// Default buffer size is used if 0.
	ReadBufferSize int

	// Per-connection buffer size for responses' writing.
	//
	// Default buffer size is used if 0.
	WriteBufferSize int

	// Maximum duration for full request reading (including body).
	//
	// By default request read timeout is unlimited.
	ReadTimeout time.Duration

	// Maximum duration for full response writing (including body).
	//
	// By default response write timeout is unlimited.
	WriteTimeout time.Duration

	// Maximum number of concurrent client connections allowed per IP.
	//
	// By default unlimited number of concurrent connections
	// may be established to the server from a single IP address.
	MaxConnsPerIP int

	// Logger, which is used by ServerCtx.Logger().
	//
	// By default standard logger from log package is used.
	Logger Logger

	perIPConnCounter perIPConnCounter
	serverName       atomic.Value

	ctxPool     sync.Pool
	readersPool sync.Pool
	writersPool sync.Pool
}

const defaultConcurrency = 64 * 1024

// Serve serves incoming connections from the given listener.
//
// Serve blocks until the given listener returns permanent error.
// This error is returned from Serve.
func (s *Server) Serve(ln net.Listener) error {
	return s.ServerConcurrency(ln, defaultConcurrency)
}

// ServeConcurrency serves incoming connections from the given listener.
// It may serve maximum concurrency simultaneous connections.
//
// ServeConcurrency blocks until the given listener returns permanent error.
// This error is returned from ServeConcurrency.
func (s *Server) ServerConcurrency(ln net.Listener, concurrency int) error {
	ch := make(chan net.Conn, 2*concurrency)
	stopCh := make(chan struct{})
	go connWorkersMonitor(s, ch, concurrency, stopCh)
	var lastOverflowErrorTime time.Time
	var lastPerIPErrorTime time.Time
	for {
		c, err := acceptConn(s, ln, &lastPerIPErrorTime)
		if err != nil {
			close(stopCh)
			return err
		}
		select {
		case ch <- c:
		default:
			c.Close()
			if time.Since(lastOverflowErrorTime) > time.Minute {
				s.logger().Printf("The incoming connection cannot be served, because all %d workers are busy. "+
					"Try increasing concurrency in Server.ServeWorkers()", concurrency)
				lastOverflowErrorTime = time.Now()
			}
		}
	}
}

var defaultLogger = log.New(os.Stderr, "", log.LstdFlags)

func (s *Server) logger() Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return defaultLogger
}

// ServeConn serves HTTP requests from the given connection.
//
// ServeConn returns nil if all requests from the c are successfully served.
// It returns non-nil error otherwise.
//
// Connection c must immediately propagate all the data passed to Write()
// to the client. Otherwise requests' processing may hang.
//
// ServeConn closes c before returning.
func (s *Server) ServeConn(c io.ReadWriteCloser) error {
	conn, ok := c.(net.Conn)
	if ok {
		pic := wrapPerIPConn(s, conn)
		if pic == nil {
			c.Close()
			return ErrPerIPConnLimit
		}
		c = pic
	}
	return s.serveConn(c)
}

func (s *Server) serveConn(c io.ReadWriteCloser) error {
	var rd readDeadliner
	readTimeout := s.ReadTimeout
	if readTimeout > 0 {
		rd, _ = c.(readDeadliner)
	}

	var wd writeDeadliner
	writeTimeout := s.WriteTimeout
	if writeTimeout > 0 {
		wd, _ = c.(writeDeadliner)
	}

	ctx := s.acquireCtx(c)
	var br *bufio.Reader
	var bw *bufio.Writer
	var dt time.Duration
	var prevReadTime time.Time

	var err error
	for {
		if rd != nil {
			if err = rd.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				break
			}
			if dt < time.Second || br != nil {
				if br == nil {
					br = acquireReader(ctx)
				}
				err = ctx.Request.Read(br)
				if br.Buffered() == 0 || err != nil {
					releaseReader(ctx, br)
					br = nil
				}
			} else {
				ctx, br, err = acquireByteReader(ctx)
				if err == nil {
					err = ctx.Request.Read(br)
					if br.Buffered() == 0 || err != nil {
						releaseReader(ctx, br)
						br = nil
					}
				}
			}
		} else {
			if dt < time.Second || br != nil {
				if br == nil {
					br = acquireReader(ctx)
				}
				if err = ctx.Request.ReadTimeout(br, readTimeout); err == ErrReadTimeout {
					// ctx.Request and br cannot be used after ErrReadTimeout.
					ctx = makeCtxShadow(ctx)
					break
				}
				if br.Buffered() == 0 {
					releaseReader(ctx, br)
					br = nil
				}
			} else {
				ctx, br, err = acquireByteReader(ctx)
				if err == nil {
					if err = ctx.Request.ReadTimeout(br, readTimeout); err == ErrReadTimeout {
						// ctx.Request and br cannot be used after ErrReadTimeout.
						ctx = makeCtxShadow(ctx)
						break
					}
					if br.Buffered() == 0 || err != nil {
						releaseReader(ctx, br)
						br = nil
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			break
		}

		dt = time.Since(prevReadTime)
		prevReadTime = time.Now()

		ctx.ID++
		ctx.Time = time.Now()
		ctx.Response.Clear()
		s.Handler(ctx)
		shadow := ctx.shadow
		if shadow != nil {
			ctx = shadow
		}
		if wd != nil {
			if err = wd.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				break
			}
		}
		if bw == nil {
			bw = acquireWriter(ctx)
		}
		if err = writeResponse(ctx, bw); err != nil {
			break
		}
		connectionClose := ctx.Response.Header.ConnectionClose

		trimBigBuffers(ctx)

		if br == nil || connectionClose {
			err = bw.Flush()
			releaseWriter(ctx, bw)
			bw = nil
			if err != nil {
				break
			}
			if connectionClose {
				break
			}
		}
	}

	if br != nil {
		releaseReader(ctx, br)
	}
	if bw != nil {
		releaseWriter(ctx, bw)
	}

	if err != nil && !strings.Contains(err.Error(), "connection reset by peer") {
		ctx.Logger().Printf("Error when serving network connection: %s", err)
	}
	s.releaseCtx(ctx)

	err1 := c.Close()
	if err == nil {
		err = err1
	}
	return err
}

var globalCtxID uint64

func (s *Server) acquireCtx(c io.ReadWriteCloser) *RequestCtx {
	v := s.ctxPool.Get()
	var ctx *RequestCtx
	if v == nil {
		ctx = &RequestCtx{
			s: s,
		}
		ctx.logger.ctx = ctx
		ctx.v = ctx
		v = ctx
	} else {
		ctx = v.(*RequestCtx)
	}
	ctx.ID = (atomic.AddUint64(&globalCtxID, 1)) << 32
	ctx.c = c
	return ctx
}

func (s *Server) releaseCtx(ctx *RequestCtx) {
	if ctx.shadow != nil {
		panic("BUG: cannot release RequestCtx with shadow")
	}
	ctx.c = nil
	ctx.fbr.c = nil
	s.ctxPool.Put(ctx.v)
}

func (s *Server) getServerName() []byte {
	v := s.serverName.Load()
	var serverName []byte
	if v == nil {
		serverName = []byte(s.Name)
		if len(serverName) == 0 {
			serverName = defaultServerName
		}
		s.serverName.Store(serverName)
	} else {
		serverName = v.([]byte)
	}
	return serverName
}

///////////////////////////////////////////////////
// conn functions
///////////////////////////////////////////////////

func connWorkersMonitor(s *Server, ch <-chan net.Conn, maxWorkers int, stopCh <-chan struct{}) {
	workersCount := uint32(0)
	var tc *time.Timer
	for {
		n := int(atomic.LoadUint32(&workersCount))
		pendingConns := len(ch)
		if n < maxWorkers && pendingConns > 0 {
			for i := 0; i < pendingConns; i++ {
				atomic.AddUint32(&workersCount, 1)
				go func() {
					connWorker(s, ch)
					atomic.AddUint32(&workersCount, ^uint32(0))
				}()
			}
			runtime.Gosched()
		} else {
			tc = initTimer(tc, 100*time.Millisecond)
			select {
			case <-stopCh:
				stopTimer(tc)
				return
			case <-tc.C:
				stopTimer(tc)
			}
		}
	}
}

func connWorker(s *Server, ch <-chan net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.logger().Printf("panic: %s\nStack trace:\n%s", r, debug.Stack())
		}
	}()

	var c net.Conn
	var tc *time.Timer
	for {
		select {
		case c = <-ch:
		default:
			tc = initTimer(tc, time.Second)
			select {
			case c = <-ch:
				stopTimer(tc)
			case <-tc.C:
				stopTimer(tc)
				return
			}
		}
		s.serveConn(c)
		c = nil
	}
}

func acceptConn(s *Server, ln net.Listener, lastPerIPErrorTime *time.Time) (net.Conn, error) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				s.logger().Printf("Temporary error when accepting new connections: %s", netErr)
				time.Sleep(time.Second)
				continue
			}
			if err != io.EOF {
				s.logger().Printf("Permanent error: %s", err)
			}
			return nil, err
		}
		if s.MaxConnsPerIP > 0 {
			pic := wrapPerIPConn(s, c)
			if pic == nil {
				c.Close()
				if time.Since(*lastPerIPErrorTime) > time.Minute {
					s.logger().Printf("The number of connections from %s exceeds MaxConnsPerIP=%d",
						getConnIP4(c), s.MaxConnsPerIP)
					*lastPerIPErrorTime = time.Now()
				}
				continue
			}
			return pic, nil
		}
		return c, nil
	}
}

func wrapPerIPConn(s *Server, c net.Conn) net.Conn {
	ip := getUint32IP(c)
	if ip == 0 {
		return c
	}
	n := s.perIPConnCounter.Register(ip)
	if n > s.MaxConnsPerIP {
		s.perIPConnCounter.Unregister(ip)
		return nil
	}
	return &perIPConn{
		Conn:             c,
		ip:               ip,
		perIPConnCounter: &s.perIPConnCounter,
	}
}

// ErrPerIPConnLimit may be returned from ServeConn if the number of connections
// per ip exceeds Server.MaxConnsPerIP
var ErrPerIPConnLimit = errors.New("too many connections per ip")

// RequestCtx contains incoming request and manages outgoing response.
//
// It is forbidden copying RequestCtx instances.
//
// RequestHandler should avoid holding references to incoming RequestCtx and/or
// its members after the return
// If holding RequestCtx references after the return is unavoidable
// (for instance, ctx is passed to a separate goroutine and we cannot control
// ctx lifetime in this goroutine), then the RequestHandler MUST call
// ctx.TimeoutError() before return
type RequestCtx struct {
	// Incoming request.
	Request Request

	// Outgoing response.
	Response Response

	// Unique id of the request.
	ID uint64

	// Start time for the request processing
	Time time.Time

	// Cache for arbitrary data, which may be used by RequestHandler.
	// Cache contents may survive across requests
	Cache interface{}

	logger ctxLogger
	s      *Server
	c      io.ReadWriteCloser
	fbr    firstByteReader

	// shadow is set by TimeoutError().
	shadow *RequestCtx

	timeoutCh    chan struct{}
	timeoutTimer *time.Timer

	v interface{}
}

var zeroIPAddr = &net.IPAddr{
	IP: net.IPv4zero,
}

// RemoteAddr returns client address for the given request.
func (ctx *RequestCtx) RemoteAddr() net.Addr {
	x, ok := ctx.c.(remoteAddrer)
	if !ok {
		return zeroIPAddr
	}
	return x.RemoteAddr()
}

// RemoteIP returns client ip for the given request.
func (ctx *RequestCtx) RemoteIP() net.IP {
	x, ok := ctx.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return net.IPv4zero
	}

	return x.IP
}

// Error sets response status code to the given value and sets response body
// to the given message.
//
// Error calls are ignored after TimeoutError call.
func (ctx *RequestCtx) Error(msg string, statusCode int) {
	resp := &ctx.Response
	resp.Clear()
	resp.Header.StatusCode = statusCode
	resp.Header.SetCanonical(strContentType, defaultContentType)
	resp.Body = append(resp.Body, []byte(msg)...)
}

// Success sets response Content-Type and body to the given values.
//
// It is safe modifying body buffer after the Success() call.
//
// Success calls are ignored after TimeoutError call.
func (ctx *RequestCtx) Success(contentType string, body []byte) {
	resp := &ctx.Response
	resp.Header.SetBytesK(strContentType, contentType)
	resp.Body = append(resp.Body, body...)
}

// Logger returns logger, which may be used for logging arbitrary
// request-specific messages inside RequestHandler.
//
// Each message logged via returned logger contains request-specific information
// such as request id, remote address, request method and request url.
//
// It is safe re-using returned logger for logging multiple messages.
func (ctx *RequestCtx) Logger() Logger {
	return &ctx.logger
}

// TimeoutError sets response status code to StatusRequestTimeout and sets
// body to the given msg.
//
// All response modifications after TimeoutError call are ignored.
//
// TimouetError MUST be called before returning from RequestsHandler if there are
// references to ctx and/or its members in other goroutines.
func (ctx *RequestCtx) TimeoutError(msg string) {
	shadow := makeCtxShadow(ctx)
	if ctx.shadow == nil {
		ctx.shadow = shadow
		shadow.Error(msg, StatusRequestTimeout)
	}
}

func writeResponse(ctx *RequestCtx, w *bufio.Writer) error {
	if ctx.shadow != nil {
		panic("BUG: cannot write response with shadow")
	}
	h := &ctx.Response.Header
	serverOld := h.server
	if len(serverOld) == 0 {
		h.server = ctx.s.getServerName()
	}
	err := ctx.Response.Write(w)
	if len(serverOld) == 0 {
		h.server = serverOld
	}
	return err
}

const bigBufferLimit = 16 * 1024

func trimBigBuffers(ctx *RequestCtx) {
	if cap(ctx.Request.Body) > bigBufferLimit {
		ctx.Request.Body = nil
	}
	if cap(ctx.Response.Body) > bigBufferLimit {
		ctx.Response.Body = nil
	}
}

var ctxLoggerLock sync.Mutex

type ctxLogger struct {
	ctx *RequestCtx
}

func (cl *ctxLogger) Printf(format string, args ...interface{}) {
	ctxLoggerLock.Lock()
	s := fmt.Sprintf(format, args...)
	ctx := cl.ctx
	req := &ctx.Request
	req.ParseURI()
	ctx.s.logger().Printf("#%016X - %s - %s %s - %s", ctx.ID, ctx.RemoteAddr(), req.Header.Method, req.URI.URI, s)
	ctxLoggerLock.Unlock()
}

const (
	defaultReadBufferSize  = 4096
	defaultWriteBufferSize = 4096
)

var bytePool sync.Pool

func acquireByteReader(ctx *RequestCtx) (*RequestCtx, *bufio.Reader, error) {
	s := ctx.s
	c := ctx.c
	s.releaseCtx(ctx)

	v := bytePool.Get()
	if v == nil {
		v = make([]byte, 1)
	}
	b := v.([]byte)
	n, err := c.Read(b)
	ch := b[0]
	bytePool.Put(v)
	ctx = s.acquireCtx(c)
	if err != nil {
		return ctx, nil, err
	}
	if n != 1 {
		panic("BUG: Reader must return at least one byte")
	}

	ctx.fbr.c = c
	ctx.fbr.ch = ch
	ctx.fbr.byteRead = false
	r := acquireReader(ctx)
	r.Reset(&ctx.fbr)
	return ctx, r, nil
}

func acquireReader(ctx *RequestCtx) *bufio.Reader {
	v := ctx.s.readersPool.Get()
	if v == nil {
		n := ctx.s.ReadBufferSize
		if n <= 0 {
			n = defaultReadBufferSize
		}
		return bufio.NewReaderSize(ctx.c, n)
	}
	r := v.(*bufio.Reader)
	r.Reset(ctx.c)
	return r
}

func releaseReader(ctx *RequestCtx, r *bufio.Reader) {
	r.Reset(nil)
	ctx.s.readersPool.Put(r)
}

func acquireWriter(ctx *RequestCtx) *bufio.Writer {
	v := ctx.s.writersPool.Get()
	if v == nil {
		n := ctx.s.WriteBufferSize
		if n <= 0 {
			n = defaultWriteBufferSize
		}
		return bufio.NewWriterSize(ctx.c, n)
	}
	w := v.(*bufio.Writer)
	w.Reset(ctx.c)
	return w
}

func releaseWriter(ctx *RequestCtx, w *bufio.Writer) {
	w.Reset(nil)
	ctx.s.writersPool.Put(w)
}

func makeCtxShadow(ctx *RequestCtx) *RequestCtx {
	var shadow RequestCtx
	shadow.Request = Request{}
	shadow.Response = Response{}
	shadow.logger.ctx = &shadow
	shadow.v = &shadow

	shadow.s = ctx.s
	shadow.c = ctx.c
	return &shadow
}

type firstByteReader struct {
	c        io.ReadWriteCloser
	ch       byte
	byteRead bool
}

func (r *firstByteReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	nn := 0
	if !r.byteRead {
		b[0] = r.ch
		b = b[1:]
		r.byteRead = true
		nn = 1
	}
	n, err := r.c.Read(b)
	return n + nn, err
}
