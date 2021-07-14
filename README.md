# justhttp

Fast HTTP library for Go

https://godoc.org/github.com/caser789/justhttp
- [x] connection pool
    - [ ] server worker pool
- [x] handle Connection: close
- [ ] shadow to handle timeout
- [x] http client
- [x] Request cookie
- [x] Limit max connection from the same IP
    - [x] connection pool
- [ ] Example tests.
- [ ] Load balancing client for multiple upstream hosts.
- [ ] Client with requests' pipelining support.
- [x] Reuse-port listner
- [x] Trade memory usage with CPU usage for too much keep-alive connections.

# Performance optimization tips for multi-core systems.

* Use [reuseport](https://godoc.org/github.com/valyala/fasthttp/reuseport) listener.
* Run a separate server instance per CPU core with GOMAXPROCS=1.
* Attach each server instance to a separate CPU core using [taskset](http://linux.die.net/man/1/taskset).
* Ensure the interrupts of multiqueue network card are evenly distributed between CPU cores. See [this article](https://blog.cloudflare.com/how-to-achieve-low-latency/) for details.
