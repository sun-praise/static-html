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

// drawerCSS styles the floating button and the slide-in drawer. Selectors are
// namespaced under #sth-drawer-* and critical layout properties use !important
// to resist conflicts with the user's own stylesheets.
const drawerCSS = `<style>
#sth-drawer-btn{position:fixed!important;bottom:24px!important;right:24px!important;width:48px!important;height:48px!important;border-radius:50%!important;background:#171717!important;color:#fff!important;display:flex!important;align-items:center!important;justify-content:center!important;font-size:22px!important;cursor:pointer!important;box-shadow:0 4px 12px rgba(0,0,0,.25)!important;z-index:2147483646!important;border:none!important;line-height:1!important;padding:0!important;margin:0!important;opacity:.9}
#sth-drawer-btn:hover{background:#333!important;opacity:1!important}
#sth-drawer-panel{position:fixed!important;top:0!important;right:0!important;width:320px!important;max-width:90vw!important;height:100vh!important;background:#fff!important;box-shadow:-4px 0 16px rgba(0,0,0,.15)!important;z-index:2147483647!important;transform:translateX(100%)!important;transition:transform .25s ease!important;overflow-y:auto!important;box-sizing:border-box!important;padding:0!important;margin:0!important;font-family:-apple-system,"Segoe UI",sans-serif!important;color:#171717!important;display:block!important}
#sth-drawer-panel.sth-drawer-open{transform:translateX(0)!important}
.sth-drawer-header{display:flex!important;align-items:center!important;justify-content:space-between!important;padding:16px!important;border-bottom:1px solid #eee!important;position:sticky!important;top:0!important;background:#fff!important;z-index:1!important}
.sth-drawer-title{font-weight:600!important;font-size:15px!important}
#sth-drawer-close{border:none!important;background:none!important;font-size:18px!important;cursor:pointer!important;color:#888!important;padding:4px 8px!important;line-height:1!important}
#sth-drawer-close:hover{color:#171717!important}
#sth-drawer-content{padding:16px!important}
.sth-drawer-section{margin-bottom:20px!important}
.sth-drawer-section h3{font-size:12px!important;text-transform:uppercase!important;letter-spacing:.05em!important;color:#888!important;margin:0 0 8px!important;font-weight:600!important}
.sth-drawer-section ul{list-style:none!important;padding:0!important;margin:0!important}
.sth-drawer-section li{margin:0!important}
.sth-drawer-section a{display:block!important;padding:8px 10px!important;border-radius:6px!important;text-decoration:none!important;color:#171717!important;font-size:14px!important}
.sth-drawer-section a:hover{background:#f5f5f5!important}
.sth-drawer-empty,.sth-drawer-loading,.sth-drawer-error{font-size:13px!important;color:#999!important;padding:8px 10px!important;margin:0!important}
.sth-drawer-error{color:#dc2626!important}
.sth-drawer-retry{margin-top:8px!important;padding:6px 12px!important;border:1px solid #d6d1c6!important;border-radius:6px!important;background:#f2efe6!important;cursor:pointer!important;font-size:13px!important}
.sth-drawer-home{display:block!important;padding:12px 10px!important;margin-top:8px!important;border-top:1px solid #eee!important;text-decoration:none!important;color:#171717!important;font-size:14px!important;font-weight:500!important}
.sth-drawer-home:hover{background:#f5f5f5!important}
</style>`

// drawerHTML is the floating button plus the drawer shell. The content area is
// populated lazily by drawerJS when the drawer is first opened.
const drawerHTML = `<div id="sth-drawer-btn" title="Browse related documents">&#9638;</div>
<aside id="sth-drawer-panel" aria-hidden="true">
  <div class="sth-drawer-header">
    <span class="sth-drawer-title">Related Documents</span>
    <button id="sth-drawer-close" title="Close" aria-label="Close">&#10005;</button>
  </div>
  <div id="sth-drawer-content"></div>
</aside>`

// drawerJS wires up the button: on first click it fetches peers from the API
// and caches the result; subsequent opens reuse the cached data. It renders
// loading, error (with retry), and empty states.
const drawerJS = `<script>
(function(){
  var btn=document.getElementById('sth-drawer-btn');
  var panel=document.getElementById('sth-drawer-panel');
  if(!btn||!panel)return;
  var content=document.getElementById('sth-drawer-content');
  var closeBtn=document.getElementById('sth-drawer-close');
  var sid=location.pathname.split('/')[2];
  var loaded=false;

  function openDrawer(){
    btn.style.display='none';
    panel.classList.add('sth-drawer-open');
    panel.setAttribute('aria-hidden','false');
    if(!loaded){loadPeers();}
  }
  function closeDrawer(){
    panel.classList.remove('sth-drawer-open');
    panel.setAttribute('aria-hidden','true');
    btn.style.display='flex';
  }
  function loadPeers(){
    loaded=true;
    content.innerHTML='<p class="sth-drawer-loading">Loading...</p>';
    fetch('/api/sessions/'+encodeURIComponent(sid)+'/peers')
      .then(function(r){if(!r.ok){throw new Error('HTTP '+r.status);}return r.json();})
      .then(function(data){render(data);})
      .catch(function(err){renderError(err);});
  }
  function render(data){
    var html='';
    html+=section('Same Category',data.byCategory,data.current.category);
    html+=section('Same Project',data.byProject,data.current.project);
    html+='<a class="sth-drawer-home" href="/">&#127968; Back to Home</a>';
    content.innerHTML=html;
  }
  function section(title,peers,label){
    var h='<div class="sth-drawer-section"><h3>'+escapeHtml(title)+(label?' ('+escapeHtml(label)+')':'')+'</h3>';
    if(!peers||peers.length===0){
      h+='<p class="sth-drawer-empty">No documents found.</p>';
    }else{
      h+='<ul>';
      for(var i=0;i<peers.length;i++){
        h+='<li><a href="/s/'+encodeURIComponent(peers[i].sessionId)+'/">'+escapeHtml(peers[i].name)+'</a></li>';
      }
      h+='</ul>';
    }
    h+='</div>';
    return h;
  }
  function renderError(err){
    content.innerHTML='<p class="sth-drawer-error">Failed to load: '+escapeHtml(String(err))+'</p><button class="sth-drawer-retry" id="sth-drawer-retry">Retry</button>';
    var retry=document.getElementById('sth-drawer-retry');
    if(retry){retry.addEventListener('click',function(){loaded=false;loadPeers();});}
  }
  function escapeHtml(s){
    return String(s).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});
  }
  btn.addEventListener('click',openDrawer);
  closeBtn.addEventListener('click',closeDrawer);
})();
</script>`

var scriptBytes = []byte(liveReloadScript)
var drawerBytes = []byte(drawerCSS + drawerHTML + drawerJS)
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

		injected := injectScript(bw.buf.Bytes(), scriptBytes, drawerBytes)
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

// injectScript inserts the given snippets into an HTML document body. The
// snippets are concatenated and inserted just before the last </head> tag when
// present, otherwise before the last </body> tag, otherwise appended to the end.
func injectScript(body []byte, snippets ...[]byte) []byte {
	var combined bytes.Buffer
	for _, snippet := range snippets {
		combined.Write(snippet)
	}
	script := combined.Bytes()

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
