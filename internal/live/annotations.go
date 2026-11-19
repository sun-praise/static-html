package live

// Annotation injection: a floating button, selection bubble, margin cards and
// a side drawer that let readers highlight text and keep personal notes on a
// preview page. Highlights render through the CSS Custom Highlight API (zero
// DOM mutation); annotations persist server-side per session and per reader
// via /api/sessions/{id}/annotations.
//
// Styling follows the drawer conventions in inject.go: #sth-ann-* selectors,
// !important on critical properties to resist page styles, and top-range
// z-indexes.

const annotationCSS = `<style>
#sth-ann-fab{position:fixed!important;bottom:24px!important;right:144px!important;width:48px!important;height:48px!important;border-radius:50%!important;background:#d97706!important;color:#fff!important;display:flex!important;align-items:center!important;justify-content:center!important;font-size:20px!important;cursor:pointer!important;box-shadow:0 4px 12px rgba(0,0,0,.25)!important;z-index:2147483646!important;border:none!important;line-height:1!important;padding:0!important;margin:0!important;font-family:-apple-system,"Segoe UI",sans-serif!important;opacity:.9}
#sth-ann-fab:hover{background:#b45309!important;opacity:1!important}
#sth-ann-fab .sth-ann-badge{position:absolute!important;top:-6px!important;right:-6px!important;background:#fff!important;color:#b45309!important;border:1px solid #e5e7eb!important;border-radius:10px!important;font-size:11px!important;font-weight:600!important;padding:0 6px!important;line-height:16px!important;min-width:16px!important;text-align:center!important}
.sth-ann-bubble{position:absolute!important;z-index:2147483646!important;display:none!important;gap:4px!important;background:#171717!important;border-radius:10px!important;padding:5px!important;box-shadow:0 8px 24px rgba(0,0,0,.35)!important;font-family:-apple-system,"Segoe UI",sans-serif!important}
.sth-ann-bubble.sth-ann-on{display:flex!important}
.sth-ann-bubble button{background:transparent!important;border:none!important;color:#f6f4ee!important;padding:4px 12px!important;border-radius:8px!important;font-size:13px!important;white-space:nowrap!important;cursor:pointer!important}
.sth-ann-bubble button:hover{background:#333!important;color:#fff!important}
.sth-ann-editor{position:absolute!important;z-index:2147483646!important;width:340px!important;max-width:90vw!important;background:#fff!important;border:1px solid #e5e7eb!important;border-radius:12px!important;padding:12px!important;box-shadow:0 20px 50px rgba(0,0,0,.18)!important;font-family:-apple-system,"Segoe UI",sans-serif!important;color:#171717!important;box-sizing:border-box!important}
.sth-ann-editor textarea{width:100%!important;height:72px!important;border:1px solid #e5e7eb!important;border-radius:8px!important;padding:6px 8px!important;font:inherit!important;font-size:13px!important;background:#fafafa!important;resize:vertical!important;box-sizing:border-box!important}
.sth-ann-editor .sth-ann-erow{display:flex!important;align-items:center!important;gap:8px!important;margin-top:10px!important}
.sth-ann-editor .sth-ann-elabel{flex:none!important;font-size:12px!important;color:#6b7280!important;white-space:nowrap!important}
.sth-ann-editor .sth-ann-tagin{flex:1!important;min-width:0!important;font-size:12px!important;padding:5px 8px!important;border:1px solid #e5e7eb!important;border-radius:8px!important;background:#fafafa!important}
.sth-ann-editor .sth-ann-ebtns{justify-content:flex-end!important}
.sth-ann-editor button{flex:none!important;white-space:nowrap!important;border-radius:8px!important;font-size:12px!important;padding:6px 16px!important;cursor:pointer!important}
.sth-ann-editor .sth-ann-ok{background:#171717!important;color:#fff!important;border:none!important}
.sth-ann-editor .sth-ann-ok:hover{background:#333!important}
.sth-ann-editor .sth-ann-cancel{background:#f5f5f5!important;border:1px solid #e5e7eb!important;color:#171717!important}
.sth-ann-editor .sth-ann-cancel:hover{background:#e5e5e5!important}
#sth-ann-drawer{position:fixed!important;top:0!important;right:0!important;width:330px!important;max-width:90vw!important;height:100vh!important;background:#fff!important;box-shadow:-4px 0 16px rgba(0,0,0,.15)!important;z-index:2147483647!important;transform:translateX(100%)!important;transition:transform .25s ease!important;overflow-y:auto!important;box-sizing:border-box!important;padding:0!important;margin:0!important;font-family:-apple-system,"Segoe UI",sans-serif!important;color:#171717!important;display:block!important}
#sth-ann-drawer.sth-ann-open{transform:translateX(0)!important}
.sth-ann-drawer-head{display:flex!important;align-items:center!important;justify-content:space-between!important;padding:16px!important;border-bottom:1px solid #eee!important;position:sticky!important;top:0!important;background:#fff!important;z-index:1!important}
.sth-ann-drawer-title{font-weight:600!important;font-size:15px!important;display:flex!important;align-items:center!important;gap:8px!important}
.sth-ann-drawer-count{background:#dbeafe!important;color:#1e40af!important;font-size:11px!important;border-radius:10px!important;padding:1px 8px!important;font-weight:400!important}
#sth-ann-drawer-close{border:none!important;background:none!important;font-size:18px!important;cursor:pointer!important;color:#888!important;padding:4px 8px!important;line-height:1!important}
#sth-ann-drawer-close:hover{color:#171717!important}
.sth-ann-drawer-content{padding:16px!important}
.sth-ann-hint{font-size:12px!important;color:#999!important;padding:0 0 12px!important;margin:0!important}
.sth-ann-card{background:#fff!important;border:1px solid #eee!important;border-radius:10px!important;padding:10px 12px!important;font-size:13px!important;margin-bottom:10px!important}
.sth-ann-card blockquote{border-left:3px solid #fde68a!important;background:#fef9e7!important;color:#78350f!important;margin:0 0 6px!important;padding:6px 10px!important;font-size:12.5px!important;border-radius:0 8px 8px 0!important}
.sth-ann-card .sth-ann-cmt{color:#374151!important;margin:6px 0!important}
.sth-ann-card .sth-ann-meta{display:flex!important;align-items:center!important;gap:6px!important;color:#999!important;font-size:11px!important}
.sth-ann-card .sth-ann-chip{background:#dbeafe!important;color:#1e40af!important;border-radius:10px!important;padding:0 8px!important;font-style:normal!important;white-space:nowrap!important}
.sth-ann-card .sth-ann-acts{margin-left:auto!important;display:flex!important;gap:4px!important}
.sth-ann-card .sth-ann-acts button{border:none!important;background:none!important;color:#888!important;font-size:11px!important;padding:2px 6px!important;border-radius:4px!important;cursor:pointer!important}
.sth-ann-card .sth-ann-acts button:hover{background:#f5f5f5!important;color:#171717!important}
.sth-ann-missing{color:#b91c1c!important;font-size:11px!important}
#sth-ann-export{width:100%!important;background:#171717!important;color:#fff!important;border:none!important;padding:9px!important;border-radius:8px!important;font-size:13px!important;cursor:pointer!important;margin-top:6px!important}
#sth-ann-export:hover{background:#333!important}
.sth-ann-margin{position:absolute!important;top:0!important;left:0!important;width:100%!important;height:0!important;overflow:visible!important;pointer-events:none!important}
.sth-ann-mcard{position:absolute!important;width:228px!important;right:24px!important;background:#fff!important;border:1px solid #e5e7eb!important;border-radius:10px!important;padding:10px 12px!important;font-size:12px!important;box-shadow:0 2px 8px rgba(0,0,0,.08)!important;pointer-events:auto!important;font-family:-apple-system,"Segoe UI",sans-serif!important;color:#065f46!important}
.sth-ann-mcard .sth-ann-num{display:inline-flex!important;width:20px!important;height:20px!important;align-items:center!important;justify-content:center!important;background:#166534!important;color:#fff!important;border-radius:50%!important;font-size:11px!important;margin-right:6px!important}
.sth-ann-mcard .sth-ann-mtag{color:#166534!important;font-size:11px!important}
.sth-ann-mcard .sth-ann-mtext{color:#065f46!important;margin-top:4px!important;word-break:break-word!important}
.sth-ann-mcard .sth-ann-macts{position:absolute!important;top:8px!important;right:8px!important;display:none!important;gap:4px!important}
.sth-ann-mcard:hover .sth-ann-macts{display:flex!important}
.sth-ann-mcard .sth-ann-macts button{border:none!important;background:none!important;color:#9ca3af!important;font-size:11px!important;padding:0 4px!important;cursor:pointer!important}
@media (max-width:1180px){.sth-ann-mcard{display:none!important}}
.sth-ann-overlay{position:fixed!important;inset:0!important;background:rgba(0,0,0,.4)!important;z-index:2147483646!important;display:flex!important;align-items:center!important;justify-content:center!important;font-family:-apple-system,"Segoe UI",sans-serif!important}
.sth-ann-panel{width:min(720px,92vw)!important;max-height:84vh!important;background:#fff!important;border:1px solid #eee!important;border-radius:14px!important;display:flex!important;flex-direction:column!important;overflow:hidden!important;box-shadow:0 24px 60px rgba(0,0,0,.3)!important;color:#171717!important}
.sth-ann-panel .sth-ann-phead{display:flex!important;align-items:center!important;padding:14px 18px!important;border-bottom:1px solid #eee!important}
.sth-ann-panel .sth-ann-phead b{font-size:15px!important}
.sth-ann-panel .sth-ann-px{margin-left:auto!important;border:none!important;background:none!important;font-size:18px!important;color:#888!important;cursor:pointer!important}
.sth-ann-panel .sth-ann-pbody{overflow:auto!important;padding:20px 26px!important}
.sth-ann-panel .sth-ann-pfoot{display:flex!important;gap:10px!important;padding:12px 18px!important;border-top:1px solid #eee!important;background:#fafafa!important}
.sth-ann-panel .sth-ann-pfoot button{border:none!important;border-radius:8px!important;padding:8px 16px!important;font-size:13px!important;cursor:pointer!important}
.sth-ann-panel .sth-ann-primary{background:#171717!important;color:#fff!important}
.sth-ann-panel .sth-ann-ghost{background:#fff!important;border:1px solid #d1d5db!important;color:#171717!important}
.sth-ann-panel .sth-ann-ghost:hover{background:#f5f5f5!important}
.sth-ann-arch h1{font-size:19px!important;margin:0 0 4px!important}
.sth-ann-arch .sth-ann-ameta{color:#6b7280!important;font-size:12px!important;margin-bottom:16px!important}
.sth-ann-aentry{margin:16px 0!important;padding:14px 16px!important;background:#fafafa!important;border:1px solid #eee!important;border-radius:10px!important}
.sth-ann-aentry .sth-ann-idx{color:#9ca3af!important;font-size:11px!important}
.sth-ann-aentry blockquote{margin:8px 0!important;padding:8px 12px!important;border-left:3px solid #fde68a!important;background:#fef9e7!important;color:#78350f!important;font-size:13px!important;border-radius:0 8px 8px 0!important}
.sth-ann-aentry .sth-ann-thought{margin:8px 0 0!important;font-size:13.5px!important;color:#1f2937!important}
.sth-ann-aentry .sth-ann-thought:before{content:"想法 "!important;color:#9ca3af!important;font-size:11px!important;margin-right:4px!important}
.sth-ann-aentry .sth-ann-ameta2{display:flex!important;gap:8px!important;margin-top:8px!important;font-size:11px!important;color:#9ca3af!important;align-items:center!important}
.sth-ann-aentry .sth-ann-chip2{background:#dbeafe!important;color:#1e40af!important;padding:0 8px!important;border-radius:10px!important}
.sth-ann-error{color:#dc2626!important;font-size:13px!important}
.sth-ann-retry{margin-top:8px!important;padding:6px 12px!important;border:1px solid #d1d5db!important;border-radius:6px!important;background:#f5f5f5!important;cursor:pointer!important;font-size:13px!important}
::highlight(sth-ann){background-color:rgba(253,224,71,.85)!important;}
</style>`

const annotationHTML = `<div id="sth-ann-fab" title="标注与笔记">&#9998;<span class="sth-ann-badge" id="sth-ann-badge" style="display:none">0</span></div>
<aside id="sth-ann-drawer" aria-hidden="true">
  <div class="sth-ann-drawer-head">
    <span class="sth-ann-drawer-title">标注与笔记 <span class="sth-ann-drawer-count" id="sth-ann-count">0 条</span></span>
    <button id="sth-ann-drawer-close" title="Close" aria-label="Close">&#10005;</button>
  </div>
  <div class="sth-ann-drawer-content">
    <p class="sth-ann-hint">选中正文文字即可划线 / 批注；想法与标签随会话保存。</p>
    <div id="sth-ann-list"></div>
    <button id="sth-ann-export">导出存档 · Markdown</button>
  </div>
</aside>
<div class="sth-ann-margin" id="sth-ann-margin"></div>`

// annotationJS wires the annotation UI: fetch the reader's annotations, anchor
// highlights to page text via the CSS Custom Highlight API, let selections be
// underlined/annotated through a small bubble, mirror annotations as numbered
// margin cards, and expose a summary drawer with Markdown export.
const annotationJS = `<script>
(function(){
  var sid = location.pathname.split('/')[2];
  if (!sid) return;

  var anns = [];          // persisted annotations, oldest first
  var loadFailed = false; // set when the list endpoint errored
  var bubble = null, pending = null;

  function esc(s){
    return String(s == null ? '' : s).replace(/[&<>"']/g, function(c){
      return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];
    });
  }
  function fmtTime(iso){
    var m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(iso || '');
    return m ? m[2] + '-' + m[3] + ' ' + m[4] + ':' + m[5] : iso;
  }

  /* ---------- anchoring ---------- */

  // Index visible text so a quote can be located across element boundaries.
  // Whitespace runs are collapsed to single spaces; every normalized
  // character keeps a pointer to its original node+offset, so match offsets
  // in the normalized text map back to exact Range positions.
  function skipped(el){
    return !el || !!el.closest('#sth-ann-fab,#sth-ann-drawer,.sth-ann-bubble,.sth-ann-editor,.sth-ann-overlay,#sth-ann-margin,script,style,noscript');
  }
  function buildIndex(){
    var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode: function(n){
        if (skipped(n.parentElement)) return NodeFilter.FILTER_REJECT;
        return n.nodeValue.replace(/\s+/g,'').length ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
      }
    });
    var norm = '', map = [];
    while (walker.nextNode()){
      var v = walker.currentNode.nodeValue;
      for (var i = 0; i < v.length; i++){
        var ch = v[i];
        if (ch === ' ' || ch === '\t' || ch === '\r' || ch === '\n' || ch === '\f'){
          if (norm.slice(-1) !== ' '){ norm += ' '; map.push({node: walker.currentNode, off: i}); }
        } else {
          norm += ch;
          map.push({node: walker.currentNode, off: i});
        }
      }
    }
    return {norm: norm, map: map};
  }
  function pointAt(idx, normOff){
    var m = idx.map[Math.max(0, Math.min(normOff, idx.map.length - 1))];
    return {node: m.node, offset: m.off};
  }
  // Locate the best match for a quote, preferring occurrences whose
  // surrounding text matches the stored prefix/suffix anchors.
  function locateQuote(a){
    var idx = buildIndex();
    var q = a.quote.replace(/\s+/g,' ').trim();
    var candidates = [], pos = -1;
    while ((pos = idx.norm.indexOf(q, pos + 1)) !== -1) candidates.push(pos);
    if (!candidates.length) return null;
    function score(p){
      var s = 0;
      if (a.prefix){
        var pre = a.prefix.replace(/\s+/g,' ').trim().slice(-24);
        if (pre && idx.norm.slice(Math.max(0, p - pre.length), p).indexOf(pre) !== -1) s += 2;
      }
      if (a.suffix){
        var suf = a.suffix.replace(/\s+/g,' ').trim().slice(0, 24);
        if (suf && idx.norm.slice(p + q.length, p + q.length + suf.length).indexOf(suf) !== -1) s += 2;
      }
      return s;
    }
    candidates.sort(function(x, y){ return score(y) - score(x); });
    var start = candidates[0], end = start + q.length;
    try {
      var range = document.createRange();
      var sp = pointAt(idx, start), ep = pointAt(idx, end);
      range.setStart(sp.node, sp.offset);
      range.setEnd(ep.node, ep.offset);
      return range;
    } catch (e) { return null; }
  }

  var highlightsSupported = typeof Highlight !== 'undefined' && !!window.CSS && !!CSS.highlights;
  function renderHighlights(){
    if (!highlightsSupported) return;
    var h = new Highlight();
    anns.forEach(function(a){ if (a._range) h.add(a._range); });
    CSS.highlights.set('sth-ann', h);
  }

  /* ---------- rendering ---------- */

  function eachRange(a, fn){
    if (a._range) fn(a._range);
  }
  function renderBadge(){
    var b = document.getElementById('sth-ann-badge');
    b.textContent = String(anns.length);
    b.style.setProperty('display', anns.length ? 'block' : 'none', 'important');
    var c = document.getElementById('sth-ann-count');
    if (c) c.textContent = anns.length + ' 条';
  }
  function renderMargin(){
    var box = document.getElementById('sth-ann-margin');
    box.innerHTML = '';
    var lastBottom = 0;
    anns.forEach(function(a, i){
      var top = null;
      eachRange(a, function(r){
        if (top === null){
          var rect = r.getBoundingClientRect();
          top = rect.top + window.scrollY;
        }
      });
      if (top === null) return;
      var card = document.createElement('div');
      card.className = 'sth-ann-mcard';
      card.setAttribute('data-id', a.id);
      card.innerHTML = '<span class="sth-ann-num">' + (i + 1) + '</span><span class="sth-ann-mtag">' + esc(a.tag || '划线') + '</span>' +
        '<span class="sth-ann-macts"><button data-act="go">定位</button><button data-act="del">删除</button></span>' +
        '<div class="sth-ann-mtext">' + (a.comment ? esc(a.comment) : '<i style="color:#9ca3af">（仅划线）</i>') + '</div>';
      card.querySelector('[data-act=go]').addEventListener('click', function(){ scrollToAnn(a); });
      card.querySelector('[data-act=del]').addEventListener('click', function(){ deleteAnnotation(a); });
      // Append before measuring: detached nodes report offsetHeight = 0.
      box.appendChild(card);
      var h = card.offsetHeight;
      if (top < lastBottom + 12) top = lastBottom + 12;
      lastBottom = top + h + 12;
      card.style.top = top + 'px';
    });
  }
  function renderList(){
    var list = document.getElementById('sth-ann-list');
    if (loadFailed){
      list.innerHTML = '<p class="sth-ann-error">标注服务加载失败。</p><button class="sth-ann-retry" id="sth-ann-retry">重试</button>';
      document.getElementById('sth-ann-retry').addEventListener('click', loadAnnotations);
      return;
    }
    if (!anns.length){
      list.innerHTML = '<p class="sth-ann-hint">还没有标注。选中正文文字试试。</p>';
      return;
    }
    list.innerHTML = '';
    anns.forEach(function(a, i){
      var card = document.createElement('div');
      card.className = 'sth-ann-card';
      card.innerHTML = '<blockquote>' + esc(a.quote) + '</blockquote>' +
        (a.comment ? '<div class="sth-ann-cmt">' + esc(a.comment) + '</div>' : '') +
        (!a._range && !a._ranges ? '<div class="sth-ann-missing">未在当前页面找到原文（可能已改版）</div>' : '') +
        '<div class="sth-ann-meta"><i class="sth-ann-chip">' + esc(a.tag || '划线') + '</i><span>' + esc(fmtTime(a.createdAt)) + '</span>' +
        '<span class="sth-ann-acts"><button data-act="go">定位</button><button data-act="del">删除</button></span></div>';
      list.appendChild(card);
      card.querySelector('[data-act=go]').addEventListener('click', function(){ scrollToAnn(a); });
      card.querySelector('[data-act=del]').addEventListener('click', function(){ deleteAnnotation(a); });
      card._idx = i;
    });
  }
  function renderAll(){
    renderBadge();
    renderHighlights();
    renderMargin();
    renderList();
  }

  /* ---------- drawer ---------- */

  var drawer = document.getElementById('sth-ann-drawer');
  var fab = document.getElementById('sth-ann-fab');
  function openDrawer(){
    fab.style.setProperty('display', 'none', 'important');
    drawer.classList.add('sth-ann-open');
    drawer.setAttribute('aria-hidden', 'false');
    try { document.body.style.paddingRight = '344px'; } catch (e) {}
  }
  function closeDrawer(){
    drawer.classList.remove('sth-ann-open');
    drawer.setAttribute('aria-hidden', 'true');
    fab.style.setProperty('display', 'flex', 'important');
    try { document.body.style.paddingRight = ''; } catch (e) {}
  }
  fab.addEventListener('click', openDrawer);
  document.getElementById('sth-ann-drawer-close').addEventListener('click', closeDrawer);

  /* ---------- selection bubble ---------- */

  function inArticle(node){
    return node && !skipped(node.parentElement ? node.parentElement : node);
  }
  function currentSelection(){
    var sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
    var range = sel.getRangeAt(0);
    if (!inArticle(range.commonAncestorContainer)) return null;
    var text = sel.toString().replace(/\s+/g, ' ').trim();
    if (!text) return null;
    return {sel: sel, range: range, text: text};
  }
  function hideBubble(){ if (bubble) bubble.classList.remove('sth-ann-on'); pending = null; }
  function refreshBubble(){
    var c = currentSelection();
    if (!c){ hideBubble(); return; }
    if (!bubble) return;
    var startText = c.range.startContainer.nodeType === 3 ? c.range.startContainer.nodeValue : '';
    var endText = c.range.endContainer.nodeType === 3 ? c.range.endContainer.nodeValue : '';
    pending = {
      range: c.range.cloneRange(),
      text: c.text,
      prefix: startText.slice(Math.max(0, c.range.startOffset - 32), c.range.startOffset),
      suffix: endText.slice(c.range.endOffset, c.range.endOffset + 32)
    };
    bubble.classList.add('sth-ann-on');
    var rect = c.range.getBoundingClientRect();
    var bw = bubble.offsetWidth, bh = bubble.offsetHeight;
    var left = Math.max(8, Math.min(rect.left + window.scrollX + rect.width / 2 - bw / 2,
      window.scrollX + document.documentElement.clientWidth - bw - 8));
    var top = rect.top + window.scrollY - bh - 8;
    if (top < window.scrollY + 4) top = rect.bottom + window.scrollY + 8;
    bubble.style.left = left + 'px';
    bubble.style.top = top + 'px';
  }
  function setupBubble(){
    bubble = document.createElement('div');
    bubble.className = 'sth-ann-bubble';
    bubble.addEventListener('mousedown', function(e){ e.preventDefault(); });
    ['划线', '批注'].forEach(function(label){
      var b = document.createElement('button');
      b.textContent = label;
      b.addEventListener('click', function(e){
        e.stopPropagation();
        var p = pending;
        hideBubble();
        if (p) startAnnotation(p, label === '批注');
      });
      bubble.appendChild(b);
    });
    document.body.appendChild(bubble);
    var t;
    document.addEventListener('mouseup', function(){ setTimeout(refreshBubble, 10); });
    document.addEventListener('selectionchange', function(){ clearTimeout(t); t = setTimeout(refreshBubble, 250); });
    document.addEventListener('scroll', hideBubble, true);
  }

  /* ---------- editor + persistence ---------- */

  function openEditor(rect, initial, onSave){
    var p = document.createElement('div');
    p.className = 'sth-ann-editor';
    p.innerHTML = '<textarea placeholder="写点想法…"></textarea>' +
      '<div class="sth-ann-erow"><span class="sth-ann-elabel">标签</span><input class="sth-ann-tagin" placeholder="如 需求"></div>' +
      '<div class="sth-ann-erow sth-ann-ebtns"><button class="sth-ann-cancel">取消</button><button class="sth-ann-ok">保存</button></div>';
    document.body.appendChild(p);
    var ta = p.querySelector('textarea'), tag = p.querySelector('.sth-ann-tagin');
    if (initial){ ta.value = initial.comment || ''; tag.value = initial.tag || ''; }
    var left = Math.max(8, Math.min(rect.left + window.scrollX + rect.width / 2 - 170,
      window.scrollX + document.documentElement.clientWidth - 348));
    p.style.left = left + 'px';
    p.style.top = rect.bottom + window.scrollY + 8 + 'px';
    ta.focus();
    function close(){ document.removeEventListener('keydown', onKey); p.remove(); }
    function onKey(e){ if (e.key === 'Escape'){ close(); onSave(null); } }
    document.addEventListener('keydown', onKey);
    p.querySelector('.sth-ann-cancel').addEventListener('click', function(){ close(); onSave(null); });
    p.querySelector('.sth-ann-ok').addEventListener('click', function(){
      var v = {comment: ta.value.trim(), tag: tag.value.trim()};
      close(); onSave(v);
    });
  }

  function toast(msg){
    var t = document.createElement('div');
    t.className = 'sth-ann-hint';
    t.style.cssText = 'position:fixed!important;bottom:88px!important;left:50%!important;transform:translateX(-50%)!important;background:#171717!important;color:#fff!important;font-size:12px!important;padding:6px 16px!important;border-radius:16px!important;z-index:2147483646!important;box-shadow:0 8px 24px rgba(0,0,0,.25)!important';
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(function(){ t.remove(); }, 2200);
  }

  function startAnnotation(p, wantsEditor){
    function persist(comment, tag){
      fetch('/api/sessions/' + encodeURIComponent(sid) + '/annotations', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({kind: comment ? 'note' : 'underline', quote: p.text, prefix: p.prefix, suffix: p.suffix, comment: comment, tag: tag})
      }).then(function(r){
        if (r.status === 401){ toast('标注需要登录：请先登录本页再标注'); return; }
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      }).then(function(saved){
        if (!saved) return;
        saved._range = p.range;
        anns.push(saved);
        renderAll();
      }).catch(function(){
        toast('标注保存失败：服务暂不可用');
      });
    }
    if (wantsEditor){
      var rect = p.range.getBoundingClientRect();
      openEditor(rect, null, function(v){
        if (!v) return;
        persist(v.comment, v.tag);
      });
    } else {
      persist('', '');
    }
  }

  function deleteAnnotation(a){
    fetch('/api/sessions/' + encodeURIComponent(sid) + '/annotations/' + encodeURIComponent(a.id), {
      method: 'DELETE', credentials: 'same-origin'
    }).then(function(r){
      if (!r.ok) throw new Error('HTTP ' + r.status);
      anns = anns.filter(function(x){ return x !== a; });
      renderAll();
    }).catch(function(){
      toast('删除失败：服务暂不可用');
    });
  }

  function scrollToAnn(a){
    closeDrawer();
    var done = false;
    eachRange(a, function(r){
      if (done) return;
      var rect = r.getBoundingClientRect();
      if (!rect || (!rect.width && !rect.height)) return;
      done = true;
      window.scrollTo({top: rect.top + window.scrollY - Math.round(window.innerHeight / 3), behavior: 'smooth'});
    });
  }

  /* ---------- export ---------- */

  function archiveMarkdown(){
    var lines = ['# 存档 · ' + (document.title || '预览页'), '',
      '- 来源：' + location.href.split('?')[0],
      '- 存档时间：' + new Date().toISOString().replace('T', ' ').slice(0, 16),
      '- 标注数：' + anns.length, ''];
    anns.forEach(function(a, i){
      lines.push('## ' + (i + 1) + '. ' + (a.tag || '划线'), '', '> ' + a.quote, '');
      if (a.comment) lines.push('**想法**：' + a.comment, '');
      lines.push('*' + fmtTime(a.createdAt) + '*', '', '---', '');
    });
    return lines.join('\n');
  }
  function copyText(t, btn){
    function done(){ if (btn){ var o = btn.textContent; btn.textContent = '已复制 ✓'; setTimeout(function(){ btn.textContent = o; }, 1200); } }
    function fallback(){
      var ta = document.createElement('textarea');
      ta.value = t; ta.style.position = 'fixed'; ta.style.opacity = '0';
      document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy'); } catch (e) {}
      ta.remove(); done();
    }
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(t).then(done, fallback);
    else fallback();
  }
  function openArchive(){
    var ov = document.createElement('div');
    ov.className = 'sth-ann-overlay';
    var panel = document.createElement('div');
    panel.className = 'sth-ann-panel';
    panel.innerHTML = '<div class="sth-ann-phead"><b>笔记存档 · Markdown 成品预览</b><button class="sth-ann-px">&#10005;</button></div>' +
      '<div class="sth-ann-pbody"></div>' +
      '<div class="sth-ann-pfoot"><button class="sth-ann-ghost" data-act="copy">复制 Markdown</button><button class="sth-ann-ghost" data-act="dl">下载 .md</button><button class="sth-ann-primary" data-act="close">关闭</button></div>';
    ov.appendChild(panel);
    document.body.appendChild(ov);
    var body = panel.querySelector('.sth-ann-pbody');
    if (!anns.length){
      body.innerHTML = '<p class="sth-ann-hint">还没有标注。选中正文文字试试。</p>';
    } else {
      var html = '<div class="sth-ann-arch"><h1>存档 · ' + esc(document.title || '预览页') + '</h1>' +
        '<div class="sth-ann-ameta">来源：' + esc(location.href.split('?')[0]) + ' · 标注 ' + anns.length + ' 条</div>';
      anns.forEach(function(a, i){
        html += '<div class="sth-ann-aentry"><div class="sth-ann-idx">#' + (i + 1) + ' · ' + esc(fmtTime(a.createdAt)) + '</div>' +
          '<blockquote>' + esc(a.quote) + '</blockquote>' +
          (a.comment ? '<p class="sth-ann-thought">' + esc(a.comment) + '</p>' : '') +
          '<div class="sth-ann-ameta2">' + (a.tag ? '<span class="sth-ann-chip2">' + esc(a.tag) + '</span>' : '') + '</div></div>';
      });
      body.innerHTML = html + '</div>';
    }
    function close(){ ov.remove(); }
    panel.querySelector('.sth-ann-px').addEventListener('click', close);
    ov.addEventListener('click', function(e){ if (e.target === ov) close(); });
    panel.querySelector('[data-act=copy]').addEventListener('click', function(e){ copyText(archiveMarkdown(), e.target); });
    panel.querySelector('[data-act=dl]').addEventListener('click', function(){
      var blob = new Blob([archiveMarkdown()], {type: 'text/markdown;charset=utf-8'});
      var link = document.createElement('a');
      link.href = URL.createObjectURL(blob);
      link.download = 'sth-存档-' + new Date().toISOString().slice(0, 10) + '.md';
      link.click();
      setTimeout(function(){ URL.revokeObjectURL(link.href); }, 3000);
    });
    panel.querySelector('[data-act=close]').addEventListener('click', close);
  }
  document.getElementById('sth-ann-export').addEventListener('click', openArchive);

  /* ---------- boot ---------- */

  function loadAnnotations(){
    loadFailed = false;
    fetch('/api/sessions/' + encodeURIComponent(sid) + '/annotations', {credentials: 'same-origin'})
      .then(function(r){
        if (r.status === 401){
          // Auth-enabled deployment: annotations require a login. Stay quiet
          // (the page itself is still readable) and explain in the drawer.
          anns = [];
          renderBadge();
          renderHighlights();
          renderMargin();
          renderList();
          document.getElementById('sth-ann-list').innerHTML =
            '<p class="sth-ann-hint">标注需要登录后使用：登录并刷新本页即可开始。</p>';
          return null;
        }
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      })
      .then(function(data){
        if (data === null) return;
        anns = data.annotations || [];
        anns.forEach(function(a){ a._range = locateQuote(a); });
        renderAll();
      })
      .catch(function(){
        loadFailed = true;
        renderList();
        toast('标注加载失败：服务暂不可用');
      });
  }

  setupBubble();
  loadAnnotations();
  var resizeT;
  window.addEventListener('resize', function(){ clearTimeout(resizeT); resizeT = setTimeout(renderMargin, 200); });
  window.addEventListener('load', renderMargin);
})();
</script>`

// annotationBytes is appended to the injected snippets for every preview page,
// next to the related-documents and version drawers.
var annotationBytes = []byte(annotationCSS + annotationHTML + annotationJS)
