package live

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const liveReloadScript = `<script>
(function(){
  var sid = location.pathname.split('/')[2];
  var delay = 1000;
  var maxDelay = 30000;
  function connect(){
    var ws = new WebSocket((location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+'/s/'+sid+'/ws');
    ws.onopen = function(){ delay = 1000; };
    ws.onmessage = function(){ location.reload(); };
    ws.onclose = function(){
      setTimeout(function(){
        connect();
      }, delay);
      delay = Math.min(delay*2, maxDelay);
    };
    ws.onerror = function(){ ws.close(); };
  }
  connect();
})();
</script>`

var scriptBytes = []byte(liveReloadScript)
var headClose = []byte("</head>")
var bodyClose = []byte("</body>")

func InjectMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/s/") {
			h.ServeHTTP(w, r)
			return
		}

		if strings.HasSuffix(r.URL.Path, "/ws") {
			h.ServeHTTP(w, r)
			return
		}

		bw := &bufferedWriter{ResponseWriter: w}
		h.ServeHTTP(bw, r)

		if bw.hijacked {
			return
		}

		ct := bw.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			bw.Header().Set("Content-Length", strconv.Itoa(bw.buf.Len()))
			bw.realHeader(bw.code)
			bw.buf.WriteTo(bw.ResponseWriter)
			return
		}

		injected := injectScript(bw.buf.Bytes(), scriptBytes)
		bw.Header().Set("Content-Length", strconv.Itoa(len(injected)))
		bw.realHeader(bw.code)
		bw.ResponseWriter.Write(injected)
	})
}

type bufferedWriter struct {
	http.ResponseWriter
	buf       bytes.Buffer
	code      int
	wroteHdr  bool
	hijacked  bool
}

func (bw *bufferedWriter) Write(b []byte) (int, error) {
	return bw.buf.Write(b)
}

func (bw *bufferedWriter) WriteHeader(code int) {
	if !bw.wroteHdr {
		bw.code = code
		bw.wroteHdr = true
	}
}

func (bw *bufferedWriter) realHeader(code int) {
	if !bw.wroteHdr {
		bw.wroteHdr = true
		bw.code = code
	}
	bw.ResponseWriter.WriteHeader(bw.code)
}

func (bw *bufferedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	bw.hijacked = true
	return bw.ResponseWriter.(http.Hijacker).Hijack()
}

func (bw *bufferedWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(&bw.buf, r)
}

var _ http.Hijacker = (*bufferedWriter)(nil)
var _ io.ReaderFrom = (*bufferedWriter)(nil)

func injectScript(body []byte, script []byte) []byte {
	if idx := bytes.LastIndex(body, headClose); idx != -1 {
		var result bytes.Buffer
		result.Write(body[:idx])
		result.Write(script)
		result.Write(body[idx:])
		return result.Bytes()
	}

	if idx := bytes.LastIndex(body, bodyClose); idx != -1 {
		var result bytes.Buffer
		result.Write(body[:idx])
		result.Write(script)
		result.Write(body[idx:])
		return result.Bytes()
	}

	var result bytes.Buffer
	result.Write(body)
	result.Write(script)
	return result.Bytes()
}
