# justhttp

Fast HTTP library for Go

https://godoc.org/github.com/caser789/justhttp
- [x] http client
    - [ ] SessionClient with referer and cookies support
- [x] Request cookie
- [x] Limit max connection from the same IP
    - [x] connection pool
- [ ] Load balancing client for multiple upstream hosts.
- [ ] Client with requests' pipelining support.
- [x] Reuse-port listner
- [x] Trade memory usage with CPU usage for too much keep-alive connections.
- [ ] Connection: Upgrade
- [ ] WebSockets
- [ ] HTTP/2.0
- [ ] Uploaded files' parsing support

- Features
    - [ ] Handle static files
        - [x] Handle large file
    - [ ] Middleware friendly
        - [x] a {k: v} store inside RequestCtx
    - [x] hijack request
    - [x] handle Connection: close
    - [ ] shadow to handle timeout
    - [x] 100-continue
    - [x] Header: Content-Range
    - [x] Header: Range: bytes=startPos-endPos
    - [x] Inmemory Listener
    - [ ] TLS
        - [x] TLSConnectionState
    - [ ] Client
        - [ ] LB among multiple upstream hosts
- Performance
    - [ ] Use sendfile syscall
    - [x] connection pool
        - [ ] server worker pool
    - [x] compress response

# Performance optimization tips for multi-core systems.

* Use [reuseport](https://godoc.org/github.com/valyala/fasthttp/reuseport) listener.
* Run a separate server instance per CPU core with GOMAXPROCS=1.
* Attach each server instance to a separate CPU core using [taskset](http://linux.die.net/man/1/taskset).
* Ensure the interrupts of multiqueue network card are evenly distributed between CPU cores. See [this article](https://blog.cloudflare.com/how-to-achieve-low-latency/) for details.

# Fasthttp best practicies

* Do not allocate objects and buffers - just reuse them as much as possible.
  Fasthttp API design encourages this.
* [sync.Pool](https://golang.org/pkg/sync/#Pool) is your best friend.
* Either do not keep references to RequestCtx members after returning
  from RequestHandler or call RequestCtx.TimeoutError() before returning
  from RequestHandler.
* [Profile your program](http://blog.golang.org/profiling-go-programs)
  in production.
  `go tool pprof --alloc_objects your-program mem.pprof` usually gives better
  insights for optimization than `go tool pprof your-program cpu.pprof`.
* Avoid conversion between []byte and string, since this may result in memory
  allocation+copy. Fasthttp API provides functions for both []byte and string -
  use these functions instead of converting manually between []byte and string.
