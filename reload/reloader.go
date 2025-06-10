package reload

import (
	"fmt"
	"log"
	// Locally injected version of https://www.github.com/gorilla/websocket v1.5.3
	"markdown-server/websocket"
	"net/http"
	"os"
	"strings"
	"sync"
)

/***********************************************
*** FUNCTIONS AND DEFINITIONS FOR MIDDLEWARE ***
************************************************/

type Reloader struct {
	// OnReload will be called after a file changes, but before the browser reloads.
	OnReload func(path string, update bool)
	// directories to recursively watch
	directories []string
	// Endpoint defines what path the WebSocket connection is formed over.
	// It is set to "/reload_ws" by default.
	Endpoint string
	// Deprecated: see DisableCaching instead.
	AllowCaching bool
	// DisableCaching is set to true by default. Writes a "Cache-Control=no-cache" header on each response.
	//
	// Some http.Handlers, like http.FileServer, automatically write a Last-Modified header.
	// Browser will usually cache the resource if no changes occur after multiple requests.
	DisableCaching bool

	// Deprecated: Use ErrorLog instead.
	Log *log.Logger

	// Enable this logger to print debug information (when the reloads happen, etc)
	DebugLog *log.Logger

	ErrorLog *log.Logger

	// Used to upgrade connections to Websocket connections
	Upgrader websocket.Upgrader

	// Used to trigger a reload on all websocket connections at once
	cond           *sync.Cond
	startedWatcher bool
}

// New returns a new Reloader with the provided directories.
func New(directories ...string) *Reloader {
	return &Reloader{
		directories:    directories,
		Endpoint:       "/reload-ws",
		ErrorLog:       log.New(os.Stderr, "Reload: ", log.Lmsgprefix|log.Ltime),
		DebugLog:       log.New(os.Stdout, "Reload: ", log.Lmsgprefix|log.Ltime),
		Upgrader:       websocket.Upgrader{},
		DisableCaching: true,

		startedWatcher: false,
		cond:           sync.NewCond(&sync.Mutex{}),
	}
}

// Handle starts the reload middleware, watching the specified directories and injecting the script into HTML responses.
func (reload *Reloader) Handle(next http.Handler) http.Handler {
	// Only init the watcher once
	if !reload.startedWatcher {
		go reload.WatchDirectories()
		reload.startedWatcher = true
	}
	scriptToInject := InjectedScript(reload.Endpoint)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Endpoint == "/reload_ws" by default
		if r.URL.Path == reload.Endpoint {
			reload.ServeWS(w, r)
			return
		}
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
			// Only requests with Sec-Fetch-Dest == "document" will have HTML document responses.
			next.ServeHTTP(w, r)
			return
		}

		// Forward Server-Sent Events (SSE) without unnecessary copying
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			next.ServeHTTP(w, r)
			return
		}

		// set headers first so that they're sent with the initial response
		if reload.DisableCaching {
			w.Header().Set("Cache-Control", "no-cache")
		}

		wrap := newWrapResponseWriter(w, r.ProtoMajor, len(scriptToInject))

		// teeBody is a fixed-size buffer that will be used to sniff the content type
		var teeBody = &fixedBuffer{buf: make([]byte, 512)} // http.DetectContentType reads at most 512 bytes
		wrap.Tee(teeBody)

		next.ServeHTTP(wrap, r)
		contentType := wrap.Header().Get("Content-Type")

		if contentType == "" {
			contentType = http.DetectContentType(teeBody.buf)
		}

		if strings.HasPrefix(contentType, "text/html") {
			// just append the script to the end of the document
			// this is invalid HTML, but browsers will accept it anyways
			// should be fine for development purposes
			_, _ = w.Write([]byte(scriptToInject))
		}
	})
}

// ServeWS is the default websocket endpoint.
// Implementing your own is easy enough if you
// don't want to use 'gorilla/websocket'
func (reload *Reloader) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := reload.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		reload.logError("ServeWS error: %s\n", err)
		return
	}

	// Block here until next reload event
	reload.Wait()

	_ = conn.WriteMessage(websocket.TextMessage, []byte("reload"))
	_ = conn.Close()
}

func (reload *Reloader) Wait() {
	reload.cond.L.Lock()
	reload.cond.Wait()
	reload.cond.L.Unlock()
}

func InjectedScript(endpoint string) string {
	return fmt.Sprintf(`
<script>
	function retry() {
	  setTimeout(() => listen(true), 1000)
	}
	function listen(isRetry) {
      let protocol = location.protocol === "https:" ? "wss://" : "ws://"
	  let ws = new WebSocket(protocol + location.host + "%s")
	  if(isRetry) {
	    ws.onopen = () => window.location.reload()
	  }
	  ws.onmessage = function(msg) {
	    if(msg.data === "reload") {
	      window.location.reload()
	    }
	  }
	  ws.onclose = retry
	}
	listen(false)
</script>`, endpoint)
}

func (reload *Reloader) logDebug(format string, v ...any) {
	if reload.DebugLog != nil {
		reload.DebugLog.Printf(format, v...)
	}
}

func (reload *Reloader) logError(format string, v ...any) {
	if reload.ErrorLog != nil {
		reload.ErrorLog.Printf(format, v...)
	}
}

// fixedBuffer implements io.Writer and writes to a fixed-size []byte slice.
type fixedBuffer struct {
	buf []byte
	pos int
}

func (w *fixedBuffer) Write(p []byte) (n int, err error) {
	if w.pos >= len(w.buf) {
		return 0, nil
	}

	n = copy(w.buf[w.pos:], p)
	w.pos += n

	return n, nil
}
