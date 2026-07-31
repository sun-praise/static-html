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

// versionDrawerCSS / versionDrawerHTML / versionDrawerJS render the version
// timeline floating button and side drawer. They mirror the related-docs
// drawer's design: a floating badge that shows the current version number
// (e.g. "v3"), and a slide-in panel that lazily fetches
// /api/sessions/{id}/chain on first open. When the chain has only one
// version the badge is hidden — there is nothing to navigate to. Selectors
// are namespaced under #sth-version-* and critical properties use !important
// to resist conflicts with the user's own stylesheets.
const versionDrawerCSS = `<style>
#sth-version-btn{position:fixed!important;bottom:24px!important;right:84px!important;width:48px!important;height:48px!important;border-radius:50%!important;background:#1d4ed8!important;color:#fff!important;display:flex!important;align-items:center!important;justify-content:center!important;font-size:14px!important;font-weight:600!important;cursor:pointer!important;box-shadow:0 4px 12px rgba(0,0,0,.25)!important;z-index:2147483646!important;border:none!important;line-height:1!important;padding:0!important;margin:0!important;font-family:-apple-system,"Segoe UI",sans-serif!important}
#sth-version-btn:hover{background:#1e40af!important}
#sth-version-panel{position:fixed!important;top:0!important;right:0!important;width:340px!important;max-width:90vw!important;height:100vh!important;background:#fff!important;box-shadow:-4px 0 16px rgba(0,0,0,.15)!important;z-index:2147483647!important;transform:translateX(100%)!important;transition:transform .25s ease!important;overflow-y:auto!important;box-sizing:border-box!important;padding:0!important;margin:0!important;font-family:-apple-system,"Segoe UI",sans-serif!important;color:#171717!important;display:block!important}
#sth-version-panel.sth-version-open{transform:translateX(0)!important}
.sth-version-header{display:flex!important;align-items:center!important;justify-content:space-between!important;padding:16px!important;border-bottom:1px solid #eee!important;position:sticky!important;top:0!important;background:#fff!important;z-index:1!important}
.sth-version-title{font-weight:600!important;font-size:15px!important}
#sth-version-close{border:none!important;background:none!important;font-size:18px!important;cursor:pointer!important;color:#888!important;padding:4px 8px!important;line-height:1!important}
#sth-version-close:hover{color:#171717!important}
.sth-version-subtitle{font-size:12px!important;color:#888!important;margin:0 0 4px!important}
.sth-version-content{padding:16px!important}
.sth-version-timeline{position:relative!important;padding-left:20px!important}
.sth-version-timeline:before{content:""!important;position:absolute!important;left:7px!important;top:6px!important;bottom:6px!important;width:2px!important;background:#e5e7eb!important}
.sth-version-item{position:relative!important;padding:8px 0 16px!important}
.sth-version-item:before{content:""!important;position:absolute!important;left:-17px!important;top:14px!important;width:10px!important;height:10px!important;border-radius:50%!important;background:#9ca3af!important;border:2px solid #fff!important}
.sth-version-item.sth-version-current:before{background:#1d4ed8!important}
.sth-version-item.sth-version-current .sth-version-link{font-weight:600!important;color:#1d4ed8!important}
.sth-version-link{display:inline-block!important;text-decoration:none!important;color:#171717!important;font-size:14px!important;padding:2px 0!important}
.sth-version-link:hover{text-decoration:underline!important}
.sth-version-meta{font-size:12px!important;color:#666!important;margin-top:2px!important}
.sth-version-created{font-size:11px!important;color:#999!important;margin-top:1px!important}
.sth-version-diff{margin-top:6px!important;font-size:12px!important;line-height:1.5!important}
.sth-version-diff .add{color:#15803d!important}
.sth-version-diff .rm{color:#b91c1c!important}
.sth-version-diff .chng{color:#b45309!important}
.sth-version-loading,.sth-version-empty,.sth-version-error{font-size:13px!important;color:#999!important;padding:8px 0!important;margin:0!important}
.sth-version-error{color:#dc2626!important}
.sth-version-retry{margin-top:8px!important;padding:6px 12px!important;border:1px solid #d6d1c6!important;border-radius:6px!important;background:#f2efe6!important;cursor:pointer!important;font-size:13px!important}
</style>`

const versionDrawerHTML = `<div id="sth-version-btn" title="Version timeline" style="display:none">v?</div>
<aside id="sth-version-panel" aria-hidden="true">
  <div class="sth-version-header">
    <span class="sth-version-title">Version Timeline</span>
    <button id="sth-version-close" title="Close" aria-label="Close">&#10005;</button>
  </div>
  <div class="sth-version-content" id="sth-version-content"></div>
</aside>`

const versionDrawerJS = `<script>
(function(){
  var btn=document.getElementById('sth-version-btn');
  var panel=document.getElementById('sth-version-panel');
  if(!btn||!panel)return;
  var content=document.getElementById('sth-version-content');
  var closeBtn=document.getElementById('sth-version-close');
  var sid=location.pathname.split('/')[2];
  var loaded=false;

  // Probe the chain endpoint up front so the badge can be hidden when this
  // session is the only version (no timeline to navigate).
  fetch('/api/sessions/'+encodeURIComponent(sid)+'/chain',{credentials:'same-origin'})
    .then(function(r){if(!r.ok){throw new Error('HTTP '+r.status);}return r.json();})
    .then(function(data){
      if(!data.versions||data.versions.length<=1){return;}
      btn.textContent='v'+data.current.versionNo;
      btn.style.display='flex';
      cache=data;
    })
    .catch(function(){/* swallow; badge just stays hidden */});

  var cache=null;
  function openPanel(){
    btn.style.display='none';
    panel.classList.add('sth-version-open');
    panel.setAttribute('aria-hidden','false');
    if(!loaded){loaded=true;load();}
  }
  function closePanel(){
    panel.classList.remove('sth-version-open');
    panel.setAttribute('aria-hidden','true');
    if(cache){btn.style.display='flex';}
  }
  function load(){
    if(cache){render(cache);return;}
    content.innerHTML='<p class="sth-version-loading">Loading...</p>';
    fetch('/api/sessions/'+encodeURIComponent(sid)+'/chain',{credentials:'same-origin'})
      .then(function(r){if(!r.ok){throw new Error('HTTP '+r.status);}return r.json();})
      .then(function(data){cache=data;render(data);})
      .catch(function(err){renderError(err);});
  }
  function render(data){
    if(!data.versions||data.versions.length===0){
      content.innerHTML='<p class="sth-version-empty">No versions.</p>';
      return;
    }
    var byVersion={};
    for(var i=0;i<(data.metadataDiff||[]).length;i++){
      byVersion[data.metadataDiff[i].toVersion]=data.metadataDiff[i];
    }
    var subtitle=data.chain&&data.chain.project
      ?'<p class="sth-version-subtitle">'+escapeHtml(data.chain.project)+' / '+escapeHtml(data.chain.entryFile)+'</p>'
      :'';
    var html=subtitle+'<div class="sth-version-timeline">';
    // Newest first for the timeline UI.
    var versions=data.versions.slice().sort(function(a,b){return b.versionNo-a.versionNo;});
    for(var j=0;j<versions.length;j++){
      html+=item(versions[j],byVersion[versions[j].versionNo]);
    }
    html+='</div>';
    content.innerHTML=html;
  }
  function item(v,diff){
    var cls=v.current?'sth-version-item sth-version-current':'sth-version-item';
    var h='<div class="'+cls+'">';
    h+='<a class="sth-version-link" href="/s/'+encodeURIComponent(v.sessionId)+'/">v'+v.versionNo+(v.current?' (current)':'')+'</a>';
    if(v.tags&&v.tags.length){
      h+='<div class="sth-version-meta">'+v.tags.map(escapeHtml).join(', ')+'</div>';
    }
    h+='<div class="sth-version-created">'+escapeHtml(v.createdAt||'')+'</div>';
    h+=diffHtml(diff);
    h+='</div>';
    return h;
  }
  function diffHtml(diff){
    if(!diff){return '';}
    var parts=[];
    for(var i=0;i<(diff.addedTags||[]).length;i++){
      parts.push('<span class="add">+'+escapeHtml(diff.addedTags[i])+'</span>');
    }
    for(var k=0;k<(diff.removedTags||[]).length;k++){
      parts.push('<span class="rm">-'+escapeHtml(diff.removedTags[k])+'</span>');
    }
    if(diff.categoryOld!==diff.categoryNew){
      parts.push('<span class="chng">category: '+escapeHtml(diff.categoryOld||'∅')+' → '+escapeHtml(diff.categoryNew||'∅')+'</span>');
    }
    if(diff.projectOld!==diff.projectNew){
      parts.push('<span class="chng">project: '+escapeHtml(diff.projectOld||'∅')+' → '+escapeHtml(diff.projectNew||'∅')+'</span>');
    }
    if(parts.length===0){return '';}
    return '<div class="sth-version-diff">'+parts.join(' · ')+'</div>';
  }
  function renderError(err){
    content.innerHTML='<p class="sth-version-error">Failed to load: '+escapeHtml(String(err))+'</p><button class="sth-version-retry" id="sth-version-retry">Retry</button>';
    var retry=document.getElementById('sth-version-retry');
    if(retry){retry.addEventListener('click',function(){loaded=false;cache=null;load();});}
  }
  function escapeHtml(s){
    return String(s).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});
  }
  btn.addEventListener('click',openPanel);
  closeBtn.addEventListener('click',closePanel);
})();
</script>`

var scriptBytes = []byte(liveReloadScript)
var drawerBytes = []byte(drawerCSS + drawerHTML + drawerJS + versionDrawerCSS + versionDrawerHTML + versionDrawerJS)
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
