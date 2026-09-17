/* TAPD workspace: metadata only. Codex conversations remain in Codex. */
(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  let snapshot = {requirements:[],sessions:[],trees:[]}, status = {available:false}, operations = [];
  let requirementID = '', selectedID = '', collapsed = new Set(), local = [];
  let loaded = false, loading = null, graph = null, observer = null;
  let view = {x:0,y:0,scale:1}, pending = new Set();
  let initiallyFolded = '';
  let localLoaded = false, localLoading = null, localError = '', localGeneration = 0;
  const title = s => s.label || s.title || '未命名会话';
  const reqTitle = r => r.title || '未命名需求';
  const tree = () => snapshot.trees.find(t => t.requirement.id === requirementID);
  const short = (id, items = snapshot.sessions) => {
    let n = 8;
    while (n < id.length && items.some(x => x.id !== id && x.id.startsWith(id.slice(0,n)))) n++;
    return id.slice(0,n);
  };
  const uuid = () => {
    if (crypto.randomUUID) return crypto.randomUUID();
    const b=crypto.getRandomValues(new Uint8Array(16));b[6]=(b[6]&15)|64;b[8]=(b[8]&63)|128;
    const h=Array.from(b,x=>x.toString(16).padStart(2,'0')).join('');return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`;
  };
  async function request(method,path,body) {
    const r=await fetch(path,{method,headers:{'Content-Type':'application/json'},...(body === undefined ? {} : {body:JSON.stringify(body)})});
    const v=await r.json();if(!r.ok){const e=new Error(v.error||`HTTP ${r.status}`);e.operation=v.operation;throw e;}return v;
  }
  async function busy(button, fn) {
    if(button?.disabled)return;
    const text=button?.textContent;
    if(button){button.disabled=true;button.textContent='处理中…';}
    try {await fn();} catch(e){showToast(e.message,'error');const error=$('as-form-error');if(error)error.textContent=e.message;}
    finally {if(button?.isConnected){button.disabled=false;button.textContent=text;}}
  }
  function updateStatus() {
    $('as-codex-status').textContent=status.available ? `${status.version || 'Codex'} · 本机会话 · 手动刷新同步` : (status.error || 'Codex 不可用；仍可维护链接和完整 ID');
    document.querySelector('[data-as-action="sync"]').disabled=!status.available;
  }
  async function reload() {
    const results=await Promise.allSettled([
      request('GET','/api/agent-sessions'), request('GET','/api/agent-sessions/status'), request('GET','/api/agent-sessions/operations')
    ]);
    if(results[0].status==='rejected')throw results[0].reason;
    snapshot=results[0].value;snapshot.requirements ||= [];snapshot.sessions ||= [];snapshot.trees ||= [];
    status=results[1].status==='fulfilled'?results[1].value:{available:false,error:results[1].reason.message};
    if(results[2].status==='fulfilled')operations=results[2].value.operations||[];
    $('cnt-agent-sessions').textContent=snapshot.requirements.length;
    if(!snapshot.requirements.some(r=>r.id===requirementID)){requirementID=snapshot.requirements[0]?.id||'';selectedID='';collapsed.clear();}
    loaded=true;updateStatus();renderList();renderContent();renderOperations();
    if(status.available&&!localLoaded&&!localLoading)loadLocalSessions();
  }
  function ensureLoaded() {if(loading)return loading;loading=reload().catch(e=>showToast(e.message,'error')).finally(()=>loading=null);return loading;}
  function updateHash(){history.replaceState(null,'',`#agent-sessions${requirementID?'?tapd='+encodeURIComponent(requirementID):''}`);}
  function renderList() {
    const q=$('as-search').value.trim().toLowerCase();
    const list=snapshot.requirements.filter(r=>{
      const t=snapshot.trees.find(x=>x.requirement.id===r.id);
      return [r.title,r.url,r.notes,r.id,...(t?.nodes||[]).flatMap(n=>[n.id,n.title,n.label])].join(' ').toLowerCase().includes(q);
    });
    $('as-requirement-list').innerHTML=list.length?list.map(r=>{
      const count=(snapshot.trees.find(t=>t.requirement.id===r.id)?.nodes||[]).filter(n=>!n.context_only).length;
      return `<button type="button" class="as-requirement ${r.id===requirementID?'selected':''}" data-as-action="select-requirement" data-id="${esc(r.id)}" aria-pressed="${r.id===requirementID}"><strong>${esc(reqTitle(r))}</strong><small title="${esc(r.url)}">${esc(r.url)}</small><small class="as-count">${count} 个会话 <span aria-hidden="true">↗</span></small></button>`;
    }).join(''):`<div class="empty" style="padding:24px 8px">${q?'没有匹配的需求':'还没有需求链接'}</div>`;
  }
  function renderContent() {
    observer?.disconnect();observer=null;graph=null;
    const t=tree();
    if(!t){$('as-content').innerHTML='<div class="as-welcome"><span class="as-welcome-symbol"><svg viewBox="0 0 24 24" width="36" height="36" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><circle cx="6" cy="5" r="2"/><circle cx="18" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><path d="M6 7v10M18 7v2a4 4 0 0 1-4 4H6"/></svg></span><h3>让每次继续，都有来处</h3><p>添加一个 TAPD 链接，关联已有 Codex 会话，<br>让需求的上下文自然生长为一棵树。</p><button class="primary" type="button" data-as-action="new-requirement">添加第一个需求</button></div>';return;}
    const r=t.requirement;
    $('as-content').innerHTML=`<div class="as-requirement-header"><div class="as-heading-line"><div><div class="as-kicker">TAPD REQUIREMENT</div><h3>${esc(reqTitle(r))}</h3></div><div class="as-heading-actions"><button type="button" class="icon" data-as-action="edit-requirement">编辑</button><button type="button" class="icon del" data-as-action="delete-requirement">删除</button><button type="button" class="icon run" data-as-action="link">＋ 关联会话</button></div></div><a class="as-tapd-link" href="${esc(r.url)}" target="_blank" rel="noopener noreferrer" title="${esc(r.url)}">${esc(r.url)} ↗</a>${r.notes?`<p class="as-notes">${esc(r.notes)}</p>`:''}</div>
      <div class="as-graph-tools"><span>${t.nodes.filter(n=>!n.context_only).length} 个关联会话 · 实线为 fork 关系</span><div class="as-tool-group"><button type="button" class="icon" data-as-action="zoom-out" aria-label="缩小">−</button><button type="button" class="icon" data-as-action="fit" title="适配整棵树">适配视图</button><button type="button" class="icon" data-as-action="zoom-in" aria-label="放大">＋</button><button type="button" class="icon" data-as-action="expand-all">展开全部</button></div></div>
      <div class="as-canvas" id="as-canvas" aria-label="TAPD 会话关系画布"><div class="as-world" id="as-world" role="tree" aria-label="需求与 fork 会话树"></div></div>
      <div class="as-graph-hint">拖动画布平移 · ＋ / − 缩放 · 节点下方按钮折叠 · 虚线节点仅用于说明祖先关系</div><div id="as-detail"></div>`;
    if(!t.nodes.some(n=>n.id===selectedID))selectedID='';
    renderGraph();renderDetail();bindCanvas();
    observer=new ResizeObserver(()=>{if($('as-canvas')?.clientWidth)fit();});observer.observe($('as-canvas'));
  }
  function renderGraph(keepView=false) {
    const t=tree();if(!t||!$('as-world'))return;
    const root={id:'tapd-root',root:true,children:[]};const byID=new Map(t.nodes.map(n=>[n.id,{...n,children:[]} ]));
    for(const n of byID.values()){const parent=byID.get(n.forked_from_id)||root;parent.children.push(n);}
    if(initiallyFolded!==requirementID){const stack=[[root,0]];while(stack.length){const [n,level]=stack.pop();if(level>=3&&n.children.length)collapsed.add(n.id);for(const child of n.children)stack.push([child,level+1]);}initiallyFolded=requirementID;}
    const W=224,H=108,G=28,LEVEL=164;const placed=[];
    function measure(n){const kids=collapsed.has(n.id)?[]:n.children;n.width=Math.max(W,kids.reduce((v,k)=>v+measure(k),0)+Math.max(0,kids.length-1)*G);return n.width;}
    measure(root);
    function place(n,left,level,parent=null){n.x=left+(n.width-W)/2;n.y=level*LEVEL;n.level=level;n.parent=parent;placed.push(n);let x=left;if(!collapsed.has(n.id))for(const k of n.children){place(k,x,level+1,n);x+=k.width+G;}}
    place(root,0,0);
    graph={width:root.width,height:Math.max(...placed.map(n=>n.y))+H+12,placed,byID};
    const edges=placed.filter(n=>n.parent).map(n=>{const x1=n.parent.x+W/2,y1=n.parent.y+H,x2=n.x+W/2,y2=n.y,mid=(y1+y2)/2;return `<path class="as-edge" d="M ${x1} ${y1} V ${mid} H ${x2} V ${y2}"/>`;}).join('');
    $('as-world').innerHTML=`<svg class="as-edges" width="${graph.width}" height="${graph.height}" aria-hidden="true">${edges}</svg>`+placed.map(n=>{
      const name=n.root?reqTitle(t.requirement):title(n);const siblings=n.parent?.children||[root];
      return `<div class="as-node ${n.root?'root':''} ${n.context_only?'context':''} ${n.id===selectedID?'selected':''}" role="treeitem" aria-level="${n.level+1}" aria-posinset="${siblings.indexOf(n)+1}" aria-setsize="${siblings.length}" aria-selected="${n.id===selectedID}" ${n.children.length?`aria-expanded="${!collapsed.has(n.id)}"`:''} tabindex="${n.id===selectedID||(!selectedID&&n.root)?0:-1}" data-as-action="select-node" data-id="${esc(n.id)}" style="left:${n.x}px;top:${n.y}px" aria-label="${esc(name)} ${n.root?'TAPD 需求':esc(n.id)}"><span class="as-node-title" title="${esc(name)}">${esc(name)}</span><code>${n.root?'TAPD':esc(short(n.id,t.nodes))}</code><span class="as-node-meta"><span>${n.root?'需求入口':n.context_only?'祖先 · 未关联':'CODEX'}</span><span class="${!n.root&&!n.available?'unavailable':''}">${n.root?`${t.nodes.filter(x=>!x.context_only).length} 个会话`:!n.available?'本机不可用':n.archived?'已归档':'本机可用'}</span></span>${n.children.length?`<button type="button" class="as-collapse" data-as-action="collapse" data-id="${esc(n.id)}" aria-label="${collapsed.has(n.id)?'展开':'折叠'} ${esc(name)}" aria-expanded="${!collapsed.has(n.id)}">${collapsed.has(n.id)?'+':'−'}</button>`:''}</div>`;
    }).join('');
    $('as-world').style.width=graph.width+'px';$('as-world').style.height=graph.height+'px';
    if(keepView)transform();else requestAnimationFrame(fit);
  }
  function transform(){if($('as-world'))$('as-world').style.transform=`translate(${view.x}px,${view.y}px) scale(${view.scale})`;}
  function fit(){const c=$('as-canvas');if(!graph||!c||!c.clientWidth)return;const scale=Math.min(1,Math.max(.12,Math.min((c.clientWidth-48)/graph.width,(c.clientHeight-48)/graph.height)));view={scale,x:(c.clientWidth-graph.width*scale)/2,y:Math.max(24,(c.clientHeight-graph.height*scale)/2)};transform();}
  function zoom(factor){const c=$('as-canvas');if(!c)return;const next=Math.min(2,Math.max(.12,view.scale*factor)),ratio=next/view.scale;view.x=c.clientWidth/2-(c.clientWidth/2-view.x)*ratio;view.y=c.clientHeight/2-(c.clientHeight/2-view.y)*ratio;view.scale=next;transform();}
  function bindCanvas(){const c=$('as-canvas');let drag=null;c.addEventListener('pointerdown',e=>{if(e.target.closest('.as-node')||e.button!==0)return;drag={x:e.clientX,y:e.clientY,vx:view.x,vy:view.y};c.setPointerCapture(e.pointerId);});c.addEventListener('pointermove',e=>{if(drag){view.x=drag.vx+e.clientX-drag.x;view.y=drag.vy+e.clientY-drag.y;transform();}});for(const event of ['pointerup','pointercancel'])c.addEventListener(event,()=>drag=null);}
  function selectNode(id,focus=false){selectedID=id==='tapd-root'?'':id;document.querySelectorAll('.as-node').forEach(el=>{const yes=el.dataset.id===id;el.classList.toggle('selected',yes);el.setAttribute('aria-selected',String(yes));el.tabIndex=yes?0:-1;if(yes&&focus)el.focus();});renderDetail();}
  function renderDetail(){const n=tree()?.nodes.find(n=>n.id===selectedID);if(!n){$('as-detail').innerHTML='<div class="as-detail-empty">选择一个会话，查看来源、完整 ID 与继续方式。</div>';return;}
    const related=snapshot.trees.filter(t=>t.nodes.some(x=>x.id===n.id&&!x.context_only)).map(t=>reqTitle(t.requirement));
    const uncertain=operations.some(o=>o.parent_id===n.id&&['pending','unknown','created'].includes(o.state));
    $('as-detail').innerHTML=`<div class="as-detail"><div class="as-heading-line"><h4>${esc(title(n))}</h4><button type="button" class="icon" data-as-action="rename">修改名称</button></div><dl class="as-detail-grid"><div><dt>完整 session ID</dt><dd><code>${esc(n.id)}</code></dd></div><div><dt>父会话</dt><dd>${n.forked_from_id?`<button class="as-inline" data-as-action="parent" data-id="${esc(n.forked_from_id)}">${esc(n.forked_from_id)}</button>`:'独立会话'}</dd></div><div><dt>工作目录</dt><dd>${esc(n.cwd||'未读取')}</dd></div><div><dt>关联需求</dt><dd>${esc(related.join('、')||'仅作祖先上下文展示')}</dd></div><div><dt>更新时间</dt><dd>${n.updated_at?esc(new Date(n.updated_at*1000).toLocaleString()):'—'}</dd></div><div><dt>来源 / 可用性</dt><dd>${esc(n.source||'Codex')} · ${n.available?(n.archived?'已归档':'上次同步时可用'):'本机不可用'}</dd></div></dl><div class="as-detail-actions"><button type="button" class="icon" data-as-action="copy-id">复制 ID</button><button type="button" class="icon" data-as-action="copy-resume">复制 resume 命令</button><button type="button" class="icon run" data-as-action="fork" ${!status.available||!n.available||n.context_only||uncertain||pending.has(n.id)?'disabled':''}>Fork 新会话</button>${!n.context_only?'<button type="button" class="icon del" data-as-action="unlink">取消此分支关联</button>':''}</div>${uncertain?'<p class="as-detail-note">该会话有待核对或待保存的 Fork 操作，请在下方操作记录中处理。</p>':''}${!n.available?'<p class="as-detail-note">完整 ID 已保存。可在持有该会话的主机使用 resume 命令；本机找到会话后才能 Fork。</p>':''}</div>`;
  }
  function openModal(heading,html,onSubmit){const m=$('modal');m.dataset.kind='agent-session';lastFocusedElement=document.activeElement;m.innerHTML=`<h3 id="modal-title">${esc(heading)}</h3><form class="as-form" id="as-form">${html}<p id="as-form-error" class="as-form-error" role="alert"></p><div class="modal-actions"><button type="button" class="cancel" data-as-action="close-modal">取消</button><button type="submit" class="save">保存</button></div></form>`;$('modal-bg').classList.add('open');$('as-form').addEventListener('submit',e=>{e.preventDefault();if(e.currentTarget.reportValidity())busy(e.currentTarget.querySelector('[type=submit]'),()=>onSubmit(new FormData(e.currentTarget)));});requestAnimationFrame(()=>m.querySelector('input,textarea,button')?.focus());}
  function requirementForm(edit){const r=edit?tree()?.requirement:null;if(edit&&!r)return;openModal(edit?'编辑需求':'添加一个需求',`<label for="as-url">TAPD 链接 *</label><input id="as-url" name="url" type="url" required maxlength="4000" placeholder="https://tapd…" value="${esc(r?.url||'')}"><label for="as-title">需求名称</label><input id="as-title" name="title" maxlength="200" placeholder="例如：回放流程优化" value="${esc(r?.title||'')}"><label for="as-notes">备注</label><textarea id="as-notes" name="notes" maxlength="5000" placeholder="目标、调整背景或需要保留的上下文">${esc(r?.notes||'')}</textarea>`,async data=>{const v=await request(edit?'PATCH':'POST','/api/tapd-requirements'+(edit?'/'+r.id:''),Object.fromEntries(data));requirementID=v.id;selectedID='';collapsed.clear();closeForm();updateHash();await reload();showToast('需求已保存','ok');});}
  function invalidateLocalSessions() {
    localGeneration++;localLoading=null;localLoaded=false;localError='';
  }
  function loadLocalSessions(refresh=false) {
    if(localLoading&&!refresh)return localLoading;
    const generation=++localGeneration;
    localError='';
    localLoading=request('GET','/api/agent-sessions/discover'+(refresh?'?refresh=1':''))
      .then(v=>{if(generation===localGeneration){local=v.sessions||[];localLoaded=true;}})
      .catch(e=>{if(generation===localGeneration)localError=e.message;})
      .finally(()=>{if(generation===localGeneration){localLoading=null;renderPicker();}});
    renderPicker();return localLoading;
  }
  function linkForm(){const id=requirementID;if(!id)return;openModal('关联已有会话',`<label for="as-session-input">Session ID 或唯一前缀 *</label><input name="session_id" id="as-session-input" required minlength="8" maxlength="36" autocomplete="off" placeholder="粘贴 codex resume 使用的 ID"><p class="as-form-help">支持完整 ID 或至少 8 位的唯一前缀。也可以从本机会话列表选择。</p><div class="as-picker-heading"><label for="as-picker-search">查找本机会话</label><button type="button" class="icon" data-as-action="refresh-local">刷新列表</button></div><input class="as-picker-search" id="as-picker-search" placeholder="按名称、目录或 ID 搜索"><p id="as-picker-status" class="as-form-help" role="status"></p><div id="as-picker-results" class="as-picker-results"></div>`,async data=>{const v=await request('POST',`/api/tapd-requirements/${id}/sessions`,Object.fromEntries(data));selectedID=v.id;closeForm();await reload();showToast('会话已关联','ok');});
    $('as-picker-search').addEventListener('input',renderPicker);
    renderPicker();
    if(status.available)loadLocalSessions();
  }
  function renderPicker(){
    const el=$('as-picker-results');if(!el)return;
    const focused=el.contains(document.activeElement)?document.activeElement.closest('.as-picker-row')?.dataset.id:null;
    const scrollTop=el.scrollTop;
    const q=$('as-picker-search').value.toLowerCase();
    const matches=local.filter(n=>[n.id,n.title,n.cwd].join(' ').toLowerCase().includes(q));
    const selected=$('as-session-input').value;
    const refresh=document.querySelector('[data-as-action="refresh-local"]');
    refresh.disabled=!status.available||!!localLoading;
    $('as-picker-status').textContent=!status.available?'Codex 不可用；仍可粘贴完整 ID。':localError?`更新失败：${localError}。已有结果仍可选择，也可粘贴完整 ID。`:localLoading?(localLoaded?'正在更新列表，已有结果可直接选择。':'正在读取本机会话…'): `已加载 ${local.length} 个会话；新会话未出现时可刷新列表。`;
    const empty=localLoading&&!localLoaded?'正在读取本机会话…':!status.available?'请粘贴完整 Session ID':localError&&!localLoaded?'读取失败，请刷新重试或粘贴完整 ID。':'没有匹配的会话';
    el.innerHTML=matches.slice(0,150).map(n=>`<button type="button" class="as-picker-row ${n.id===selected?'selected':''}" data-as-action="pick-session" data-id="${esc(n.id)}"><strong>${esc(title(n))}${n.archived?' · 已归档':''}</strong><small>${esc(short(n.id,local))} · ${esc(n.cwd||'无工作目录')}</small></button>`).join('')||`<div class="empty" style="padding:22px">${empty}</div>`;
    if(matches.length>150)el.insertAdjacentHTML('beforeend','<p class="as-form-help">仅展示前 150 条，请输入关键字缩小范围。</p>');
    if(focused)el.querySelector(`[data-id="${CSS.escape(focused)}"]`)?.focus({preventScroll:true});
    el.scrollTop=scrollTop;
  }
  async function copy(text){if(navigator.clipboard){try{await navigator.clipboard.writeText(text);showToast('已复制','ok');return;}catch{}}
    const input=document.createElement('textarea');input.value=text;input.style.position='fixed';input.style.opacity='0';document.body.append(input);input.select();const ok=document.execCommand('copy');input.remove();if(!ok)throw new Error('复制失败，请在详情中选中完整 ID 复制');showToast('已复制','ok');}
  async function fork(id,requestID=uuid()){pending.add(id);renderDetail();try{const op=await request('POST',`/api/agent-sessions/${id}/fork`,{request_id:requestID});if(op.session)selectedID=op.session.id;showToast('子会话已创建，父子关系已保存','ok');}catch(e){showToast(e.message,'error');}finally{pending.delete(id);invalidateLocalSessions();await reload();}}
  function renderOperations(){const unresolved=operations.filter(o=>o.state!=='completed'&&o.state!=='failed');$('as-operations').innerHTML=unresolved.length?`<div class="as-operations-card"><h4>待处理的 Fork 操作</h4><p style="font-size:11px">已创建的会话只需恢复保存；结果未知时先刷新本机会话，再核对新子会话，不会自动再次 Fork。</p>${unresolved.map(o=>{const candidates=snapshot.sessions.filter(n=>n.forked_from_id===o.parent_id&&n.created_at>=o.started_at-2);return `<div class="as-operation"><strong>${o.state==='created'?'会话已创建，等待保存':o.state==='pending'?'操作进行中或结果待核对':'结果待核对'}</strong> · 父会话 <code>${esc(o.parent_id)}</code><br>请求 <code>${esc(o.request_id)}</code>${o.session?`<br>新 ID <code>${esc(o.session.id)}</code>`:''}${o.error?`<br>${esc(o.error)}`:''}<br><button class="icon" type="button" data-as-action="retry-operation" data-id="${esc(o.parent_id)}" data-request="${esc(o.request_id)}">${o.state==='created'?'恢复保存':'核对操作状态'}</button>${candidates.map(n=>`<button class="icon" type="button" data-as-action="resolve-operation" data-id="${esc(n.id)}" data-request="${esc(o.request_id)}">确认匹配：${esc(short(n.id))}</button>`).join('')}</div>`;}).join('')}</div>`:'';}
  document.addEventListener('click',e=>{
    const button=e.target.closest('[data-as-action]');if(!button||button.disabled)return;
    const action=button.dataset.asAction,id=button.dataset.id;
    if(action==='collapse')e.stopPropagation();
    switch(action){
      case 'select-requirement':requirementID=id;selectedID='';collapsed.clear();updateHash();renderList();renderContent();break;
      case 'new-requirement':requirementForm(false);break;
      case 'edit-requirement':requirementForm(true);break;
      case 'delete-requirement':if(confirm('删除这个需求及其关联记录？Codex 会话会保留。'))busy(button,async()=>{await request('DELETE','/api/tapd-requirements/'+requirementID);await reload();updateHash();});break;
      case 'sync':busy(button,async()=>{await request('POST','/api/agent-sessions/sync',{});invalidateLocalSessions();await reload();showToast('会话和 fork 关系已同步','ok');});break;
      case 'link':linkForm();break;
      case 'refresh-local':loadLocalSessions(true);break;
      case 'close-modal':closeForm();break;
      case 'pick-session':$('as-session-input').value=id;renderPicker();$('as-session-input').focus();break;
      case 'select-node':selectNode(id);break;
      case 'parent':collapsed.clear();renderGraph();selectNode(id,true);break;
      case 'collapse':if(collapsed.has(id))collapsed.delete(id);else collapsed.add(id);renderGraph(true);document.querySelector(`.as-node[data-id="${id}"]`)?.focus();break;
      case 'expand-all':collapsed.clear();initiallyFolded=requirementID;renderGraph();break;
      case 'fit':fit();break;
      case 'zoom-in':zoom(1.25);break;
      case 'zoom-out':zoom(.8);break;
      case 'copy-id':busy(button,()=>copy(selectedID));break;
      case 'copy-resume':busy(button,()=>copy('codex resume '+selectedID));break;
      case 'fork':busy(button,()=>fork(selectedID));break;
      case 'retry-operation':busy(button,()=>fork(id,button.dataset.request));break;
      case 'resolve-operation':if(confirm('将此子会话认定为该次 Fork 的结果？请先核对其 ID、父会话和创建时间。'))busy(button,async()=>{await request('POST',`/api/agent-sessions/operations/${button.dataset.request}/resolve`,{session_id:id});await reload();});break;
      case 'unlink':if(confirm('取消此会话及整个 fork 分支在当前需求下的关联？其他需求不受影响，刷新后也不会自动恢复此分支。'))busy(button,async()=>{await request('DELETE',`/api/tapd-requirements/${requirementID}/sessions/${selectedID}`);selectedID='';await reload();});break;
      case 'rename':{const n=tree()?.nodes.find(n=>n.id===selectedID);if(!n)break;openModal('修改会话名称',`<label for="as-label">本地显示名称</label><input id="as-label" name="label" maxlength="200" value="${esc(n.label||'')}" placeholder="${esc(n.title||'会话名称')}"><p class="as-form-help">此名称供 Flow 显示，所有关联需求共用。</p>`,async data=>{await request('PATCH','/api/agent-sessions/'+n.id,Object.fromEntries(data));closeForm();await reload();});break;}
    }
  });
  document.addEventListener('keydown',e=>{
    // Trap modal focus for both existing resource forms and the session forms.
    if(e.key==='Tab'&&$('modal-bg').classList.contains('open')){const elements=[...$('modal').querySelectorAll('button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href]')];if(elements.length){const first=elements[0],last=elements.at(-1);if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}return;}
    const node=e.target.closest('.as-node');if(!node||!graph||e.target.closest('button'))return;const n=graph.placed.find(n=>n.id===node.dataset.id);if(!n)return;let target;
    if(e.key==='Enter'||e.key===' '){e.preventDefault();selectNode(n.id);return;}
    if(e.key==='ArrowDown')target=graph.placed[graph.placed.indexOf(n)+1];
    if(e.key==='ArrowUp')target=graph.placed[graph.placed.indexOf(n)-1];
    if(e.key==='ArrowRight'){if(collapsed.has(n.id)){collapsed.delete(n.id);renderGraph(true);}target=n.children[0];}
    if(e.key==='ArrowLeft'){if(n.children.length&&!collapsed.has(n.id)){collapsed.add(n.id);renderGraph(true);target=n;}else target=n.parent;}
    if(e.key==='Home')target=graph.placed[0];if(e.key==='End')target=graph.placed.at(-1);
    if(target){e.preventDefault();selectNode(target.id,true);}
  });
  $('as-search').addEventListener('input',renderList);
  document.querySelector('[data-tab="agent-sessions"]').addEventListener('click',()=>{updateHash();ensureLoaded();});
  $('reload-btn').addEventListener('click',()=>{if(loaded)ensureLoaded();});
  document.querySelectorAll('nav.side button:not([data-tab="agent-sessions"])').forEach(b=>b.addEventListener('click',()=>history.replaceState(null,'','#'+b.dataset.tab)));
  function hashNavigate(){if(location.hash.startsWith('#agent-sessions')){requirementID=new URLSearchParams(location.hash.split('?')[1]||'').get('tapd')||requirementID;document.querySelector('[data-tab="agent-sessions"]').click();}}
  window.addEventListener('hashchange',hashNavigate);hashNavigate();
})();
