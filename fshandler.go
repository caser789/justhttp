package fasthttp

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// FSHandlerCacheDuration is the duration for caching open file handles
// by FSHandler.
const FSHandlerCacheDuration = 5 * time.Second

// FSHandler returns request handler serving static files from
// the given root folder.
//
// stripSlashes indicates how many leading slashes must be stripped
// from requested path before searching requested file in the root folder.
// Examples:
//
//   * stripSlashes = 0, original path: "/foo/bar", result: "/foo/bar"
//   * stripSlashes = 1, original path: "/foo/bar", result: "/bar"
//   * stripSlashes = 2, original path: "/foo/bar", result: ""
//
// FSHandler caches requested file handles for FSHandlerCacheDuration.
// Make sure your program has enough 'max open files' limit aka
// 'ulimit -n' if root folder contains many files.
//
// Do not create multiple FSHandler instances for the same (root, stripSlashes)
// arguments - just reuse a single instance. Otherwise goroutine leak
// will occur.
func FSHandler(root string, stripSlashes int) RequestHandler {
	// strip trailing slashes from the root path
	for len(root) > 0 && root[len(root)-1] == '/' {
		root = root[:len(root)-1]
	}

	// serve files from the current working directory if root is empty
	if len(root) == 0 {
		root = "."
	}

	if stripSlashes < 0 {
		stripSlashes = 0
	}

	h := &fsHandler{
		root:         root,
		stripSlashes: stripSlashes,
		cache:        make(map[string]*fsFile),
	}
	go func() {
		for {
			time.Sleep(FSHandlerCacheDuration / 2)
			h.cleanCache()
		}
	}()
	return h.handleRequest
}

type fsHandler struct {
	root         string
	stripSlashes int
	cache        map[string]*fsFile
	pendingFiles []*fsFile
	cacheLock    sync.Mutex

	fileReaderPool sync.Pool
}

type fsFile struct {
	h             *fsHandler
	f             *os.File
	dirIndex      []byte
	contentType   string
	contentLength int

	t            time.Time
	readersCount int
}

func (ff *fsFile) Reader(incrementReaders bool) io.Reader {
	if incrementReaders {
		ff.h.cacheLock.Lock()
		ff.readersCount++
		ff.h.cacheLock.Unlock()
	}

	v := ff.h.fileReaderPool.Get()
	if v == nil {
		r := &fsFileReader{
			ff: ff,
		}
		r.v = r
		return r
	}
	r := v.(*fsFileReader)
	r.ff = ff
	if r.offset > 0 {
		panic("BUG: fsFileReader with non-nil offset found in the pool")
	}

	return r
}

func (ff *fsFile) Release() {
	if ff.f != nil {
		ff.f.Close()
	}
}

type fsFileReader struct {
	ff     *fsFile
	offset int64

	v interface{}
}

func (r *fsFileReader) Close() error {
	ff := r.ff

	ff.h.cacheLock.Lock()
	ff.readersCount--
	if ff.readersCount < 0 {
		panic("BUG: negative fsFile.readersCount!")
	}
	ff.h.cacheLock.Unlock()

	r.ff = nil
	r.offset = 0
	ff.h.fileReaderPool.Put(r.v)
	return nil
}

func (r *fsFileReader) Read(p []byte) (int, error) {
	if r.ff.f != nil {
		n, err := r.ff.f.ReadAt(p, r.offset)
		r.offset += int64(n)
		return n, err
	}

	if r.offset == int64(len(r.ff.dirIndex)) {
		return 0, io.EOF
	}
	n := copy(p, r.ff.dirIndex[r.offset:])
	r.offset += int64(n)
	return n, nil
}

func (h *fsHandler) cleanCache() {
	t := time.Now()
	h.cacheLock.Lock()

	// Close files which couldn't be closed before due to non-zero
	// readers count.
	var pendingFiles []*fsFile
	for _, ff := range h.pendingFiles {
		if ff.readersCount > 0 {
			pendingFiles = append(pendingFiles, ff)
		} else {
			ff.Release()
		}
	}
	h.pendingFiles = pendingFiles

	// Close stale file handles.
	for k, ff := range h.cache {
		if t.Sub(ff.t) > FSHandlerCacheDuration {
			if ff.readersCount > 0 {
				// There are pending readers on stale file handle,
				// so we cannot close it. Put it into pendingFiles
				// so it will be closed later.
				h.pendingFiles = append(h.pendingFiles, ff)
			} else {
				ff.Release()
			}
			delete(h.cache, k)
		}
	}

	h.cacheLock.Unlock()
}

func (h *fsHandler) handleRequest(ctx *RequestCtx) {
	path := ctx.Path()
	path = stripPathSlashes(path, h.stripSlashes)

	if n := bytes.IndexByte(path, 0); n >= 0 {
		ctx.Logger().Printf("cannot serve path with nil byte at position %d: %q", n, path)
		ctx.Error("Are you a hacker?", StatusBadRequest)
		return
	}

	incrementReaders := true

	h.cacheLock.Lock()
	ff, ok := h.cache[string(path)]
	if ok {
		ff.readersCount++
		incrementReaders = false
	}
	h.cacheLock.Unlock()

	if !ok {
		pathStr := string(path)
		filePath := h.root + pathStr
		var err error
		ff, err = h.openFSFile(filePath)
		if err == errDirIndexRequired {
			ff, err = h.createDirIndex(ctx.URI(), filePath)
			if err != nil {
				ctx.Logger().Printf("Cannot create index for directory %q: %s", filePath, err)
				ctx.Error("Cannot create directory index", StatusNotFound)
				return
			}
		} else if err != nil {
			ctx.Logger().Printf("cannot open file %q: %s", filePath, err)
			ctx.Error("Cannot open requested path", StatusNotFound)
			return
		}

		h.cacheLock.Lock()
		ff1, ok := h.cache[pathStr]
		if !ok {
			h.cache[pathStr] = ff
		}
		h.cacheLock.Unlock()

		if ok {
			// The file has been already opened by another
			// goroutine, so close the current file and use
			// the file opened by another goroutine instead.
			ff.Release()
			ff = ff1
		}
	}

	ctx.SetBodyStream(ff.Reader(incrementReaders), ff.contentLength)
	ctx.SetContentType(ff.contentType)
}

var errDirIndexRequired = errors.New("directory index required")

func (h *fsHandler) createDirIndex(base *URI, filePath string) (*fsFile, error) {
	var buf bytes.Buffer
	w := &buf

	basePathEscaped := html.EscapeString(string(base.Path()))
	fmt.Fprintf(w, "<html><head><title>%s</title></head><body>", basePathEscaped)
	fmt.Fprintf(w, "<h1>%s</h1>", basePathEscaped)
	fmt.Fprintf(w, "<ul>")

	if len(basePathEscaped) > 1 {
		fmt.Fprintf(w, `<li><a href="..">..</a></li>`)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	filenames, err := f.Readdirnames(0)
	f.Close()
	if err != nil {
		return nil, err
	}

	var u URI
	base.CopyTo(&u)

	sort.Sort(sort.StringSlice(filenames))
	for _, name := range filenames {
		u.Update(name)
		pathEscaped := html.EscapeString(string(u.Path()))
		fmt.Fprintf(w, `<li><a href="%s">%s</a></li>`, pathEscaped, html.EscapeString(name))
	}

	fmt.Fprintf(w, "</ul></body></html>")
	dirIndex := w.Bytes()

	ff := &fsFile{
		h:             h,
		dirIndex:      dirIndex,
		contentType:   "text/html",
		contentLength: len(dirIndex),
	}
	return ff, nil
}

func (h *fsHandler) openFSFile(filePath string) (*fsFile, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	if stat.IsDir() {
		f.Close()

		indexPath := filePath + "/index.html"
		ff, err := h.openFSFile(indexPath)
		if err == nil {
			return ff, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		return nil, errDirIndexRequired
	}

	n := stat.Size()
	contentLength := int(n)
	if n != int64(contentLength) {
		f.Close()
		return nil, fmt.Errorf("too big file: %d bytes", n)
	}

	ext := fileExtension(filePath)
	contentType := mime.TypeByExtension(ext)

	ff := &fsFile{
		h:             h,
		f:             f,
		contentType:   contentType,
		contentLength: contentLength,
	}
	return ff, nil
}

func stripPathSlashes(path []byte, stripSlashes int) []byte {
	// strip leading slashes
	for stripSlashes > 0 && len(path) > 0 {
		if path[0] != '/' {
			panic("BUG: path must start with slash")
		}
		n := bytes.IndexByte(path[1:], '/')
		if n < 0 {
			path = path[:0]
			break
		}
		path = path[n+1:]
		stripSlashes--
	}

	// strip trailing slashes
	for len(path) > 0 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	return path
}

func fileExtension(path string) string {
	n := strings.LastIndexByte(path, '.')
	if n < 0 {
		return ""
	}
	return path[n:]
}
