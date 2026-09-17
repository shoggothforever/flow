/* Run with FLOW_UI_BINARY and an installed Playwright module; see README. */
const { chromium } = require(process.env.FLOW_PLAYWRIGHT_MODULE || 'playwright');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const assert = require('node:assert/strict');
const net = require('node:net');

(async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'flow-agent-browser-'));
  const output = process.env.FLOW_UI_ARTIFACTS || dir;
  await fs.mkdir(output, {recursive:true});
  const config = path.join(dir, 'config.json'), fixture = path.join(dir, 'sessions.json');
  const A='01a10000-1111-4000-8000-000000000001', B='01a10000-2222-4000-8000-000000000002', C='01a30000-3333-4000-8000-000000000003';
  const now=Math.floor(Date.now()/1000);
  const session=(id,name,parent='')=>({id,sessionId:A,name,cwd:'/workspace/flow',source:'cli',createdAt:now,updatedAt:now,forkedFromId:parent});
  await fs.writeFile(fixture,JSON.stringify([session(A,'需求分析与设计'),session(B,'接口方案讨论',A),session(C,'独立实现探索'),{...session('01a40000-4444-4000-8000-000000000004','内部代理',A),parentThreadId:A}]));
  await fs.copyFile(path.join(__dirname,'fake-codex.py'),path.join(dir,'codex'));await fs.chmod(path.join(dir,'codex'),0o700);
  const reservation=net.createServer();await new Promise(resolve=>reservation.listen(0,'127.0.0.1',resolve));const port=reservation.address().port;await new Promise(resolve=>reservation.close(resolve));
  const base=`http://127.0.0.1:${port}`;
  const server=spawn(process.env.FLOW_UI_BINARY||path.resolve(__dirname,'../bin/flow'),['--config',config,'ui','--addr',`127.0.0.1:${port}`,'--no-open'],{env:{...process.env,PATH:dir+path.delimiter+process.env.PATH,FLOW_CODEX_FIXTURE:fixture},stdio:['ignore','pipe','pipe']});
  let logs='';server.stdout.on('data',x=>logs+=x);server.stderr.on('data',x=>logs+=x);
  let browser;
  try {
    for(let i=0;i<100;i++){try{if((await fetch(base+'/api/meta')).ok)break;}catch{}if(i===99)throw new Error('Server did not start: '+logs);await new Promise(r=>setTimeout(r,50));}
    browser=await chromium.launch({headless:true, ...(process.env.FLOW_CHROMIUM_PATH?{executablePath:process.env.FLOW_CHROMIUM_PATH}:{})});
    const context=await browser.newContext({viewport:{width:1440,height:1000},permissions:['clipboard-read','clipboard-write']});
    const page=await context.newPage(), errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
    let discoveryRequests=0;page.on('request',r=>{if(r.url().includes('/api/agent-sessions/discover'))discoveryRequests++;});
    const listCalls=async()=>((await fs.readFile(fixture+'.calls','utf8')).match(/^thread\/list$/gm)||[]).length;
    const req=async(method,endpoint,body)=>{const r=await fetch(base+endpoint,{method,headers:{'Content-Type':'application/json'},...(body===undefined?{}:{body:JSON.stringify(body)})});assert(r.ok,await r.clone().text());return r.json();};
    const action=name=>page.locator(`[data-as-action="${name}"]`).filter({visible:true});
    const addRequirement=async(title,url)=>{await action('new-requirement').first().click();await page.locator('#as-url').fill(url);await page.locator('#as-title').fill(title);await page.locator('#as-notes').fill('需求调整时，保留每次探索的上下文与分支来源。');await page.locator('#as-form button[type=submit]').click();await page.locator('#modal-bg').waitFor({state:'hidden'});await page.locator('.as-heading-line h3').filter({hasText:title}).waitFor();};
    const addSession=async(id)=>{await action('link').click();await page.locator('#as-session-input').fill(id);await page.locator('#as-form button[type=submit]').click();await page.locator('#modal-bg').waitFor({state:'hidden'});await page.locator(`.as-node[data-id="${id}"]`).waitFor();};
    await page.goto(base+'/#agent-sessions');await action('sync').waitFor();
    await addRequirement('回放流程优化','https://tapd.example/story/123?tab=detail');
    assert(discoveryRequests>0,'workspace did not preload the session inventory');
    await action('link').click();await page.locator('#as-picker-results .as-picker-row').first().waitFor();
    assert.equal(await page.locator('#as-picker-results .as-picker-row').count(),3,'internal subagent filtered');
    // Hold the next HTTP response: reopening must show usable previous results
    // before it arrives, and the server must reuse the completed Codex scan.
    const callsBeforeReopen=await listCalls();await action('close-modal').click();
    let releaseDiscovery;const heldDiscovery=new Promise(resolve=>releaseDiscovery=resolve);
    await page.route('**/api/agent-sessions/discover',async route=>{await heldDiscovery;await route.continue();});
    await action('link').click();await page.locator('#as-picker-results .as-picker-row').first().waitFor();
    await page.locator('#as-picker-search').fill('需求分析');assert.equal(await page.locator('#as-picker-results .as-picker-row').count(),1);
    assert(!(await page.locator('#as-picker-results .as-picker-row').first().isDisabled()));
    await page.locator('#as-picker-results .as-picker-row').first().focus();
    releaseDiscovery();await page.locator('#as-picker-status').filter({hasText:'已加载'}).waitFor();
    assert.equal(await page.locator('#as-picker-results .as-picker-row:focus').getAttribute('data-id'),A,'background refresh lost keyboard focus');
    await page.unroute('**/api/agent-sessions/discover');assert.equal(await listCalls(),callsBeforeReopen,'reopening rescanned Codex');
    // A fresh external session must appear without waiting for the cache TTL.
    const pickerExtra='01a70000-7777-4000-8000-000000000007';
    const pickerInventory=JSON.parse(await fs.readFile(fixture));pickerInventory.push(session(pickerExtra,'外部新会话'));
    await fs.writeFile(fixture,JSON.stringify(pickerInventory));await page.locator('#as-picker-search').fill('');
    await action('refresh-local').click();await page.locator(`#as-picker-results [data-id="${pickerExtra}"]`).waitFor();
    assert.equal(await listCalls(),callsBeforeReopen+2,'explicit refresh did not scan active and archived sessions');
    await page.route('**/api/agent-sessions/discover?refresh=1',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'temporary scan failure'})}));
    await action('refresh-local').click();await page.locator('#as-picker-status').filter({hasText:'更新失败'}).waitFor();
    assert.equal(await page.locator('#as-picker-results .as-picker-row').count(),4,'failed refresh erased usable results');
    await page.unroute('**/api/agent-sessions/discover?refresh=1');
    await page.locator('#as-session-input').fill('01a10000');await page.locator('#as-form button[type=submit]').click();await page.locator('#as-form-error').filter({hasText:'ambiguous'}).waitFor();
    await page.locator(`#as-picker-results [data-id="${A}"]`).click();await page.locator('#as-form button[type=submit]').click();await page.locator('#modal-bg').waitFor({state:'hidden'});
    await action('fork').click();await page.locator('.as-detail h4').filter({hasText:'需求调整'}).waitFor();
    let data=await req('GET','/api/agent-sessions');const child=data.sessions.find(x=>x.forked_from_id===A);assert(child);assert.notEqual(child.id,A);
    await action('copy-resume').click();assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'codex resume '+child.id);
    await addRequirement('服务端接入与验证','https://tapd.example/story/456');await addSession(A);await addSession(C);await action('sync').click();
    await page.locator(`.as-node[data-id="${B}"]`).waitFor();
    const external='01a50000-5555-4000-8000-000000000005';let local=JSON.parse(await fs.readFile(fixture));local.push(session(external,'外部终端的验证分支',child.id));await fs.writeFile(fixture,JSON.stringify(local));
    await action('sync').click();await page.locator(`.as-node[data-id="${external}"]`).waitFor();
    await page.locator(`.as-node[data-id="${child.id}"]`).click();await action('unlink').click();await page.locator(`.as-node[data-id="${child.id}"]`).waitFor({state:'detached'});
    await action('sync').click();await page.waitForFunction(()=>!document.querySelector('[data-as-action="sync"]').disabled);
    assert.equal(await page.locator(`.as-node[data-id="${external}"]`).count(),0,'excluded branch reappeared');
    data=await req('GET','/api/agent-sessions');const first=data.trees.find(t=>t.requirement.title==='回放流程优化');assert(first.nodes.some(n=>n.id===external&&!n.context_only),'other requirement lost child');
    await page.reload();await page.locator(`.as-node[data-id="${C}"]`).waitFor();assert.equal(await page.locator(`.as-node[data-id="${external}"]`).count(),0);
    // A persisted fork with a lost RPC response is matched explicitly, never repeated.
    await page.locator(`.as-node[data-id="${A}"]`).click();const countBeforeUnknown=JSON.parse(await fs.readFile(fixture)).length;
    await fs.writeFile(fixture+'.lose-fork-response','1');await action('fork').click();await page.locator('.as-operations-card').waitFor();
    const uncertainChild=JSON.parse(await fs.readFile(fixture)).at(-1).id;
    await action('sync').click();await page.locator(`[data-as-action="resolve-operation"][data-id="${uncertainChild}"]`).click();await page.locator('.as-operations-card').waitFor({state:'detached'});
    assert.equal(JSON.parse(await fs.readFile(fixture)).length,countBeforeUnknown+1,'unknown outcome created duplicate forks');
    await page.locator(`.as-node[data-id="${A}"]`).click();await action('rename').click();await page.locator('#as-label').fill('基础方案 · 共享上下文');await page.locator('#as-form button[type=submit]').click();await page.locator('#modal-bg').waitFor({state:'hidden'});
    data=await req('GET','/api/agent-sessions');assert(data.trees.every(t=>t.nodes.find(n=>n.id===A).label==='基础方案 · 共享上下文'));assert.equal(JSON.parse(await fs.readFile(fixture)).find(n=>n.id===A).name,'需求分析与设计');
    // Deep, branching tree + hostile title remains text; metadata has no executable markup.
    local=JSON.parse(await fs.readFile(fixture));let parent=B;
    for(let i=0;i<12;i++){const id=`01b${String(i).padStart(5,'0')}-1111-4000-8000-000000000001`;local.push(session(id,i===0?'<img src=x onerror=window.__injected=1>':`验证步骤 ${i+1}`,parent));parent=id;}
    await fs.writeFile(fixture,JSON.stringify(local));await action('sync').click();await page.locator(`.as-node[data-id="${parent}"]`).waitFor();
    assert.equal(await page.evaluate(()=>window.__injected),undefined);
    await page.locator(`.as-node[data-id="${B}"] .as-collapse`).click();assert.equal(await page.locator(`.as-node[data-id="${parent}"]`).count(),0);
    await action('fit').click();await page.locator(`.as-node[data-id="${A}"]`).focus();await page.keyboard.press('ArrowRight');
    assert.equal(await page.locator('.as-node:focus').count(),1);
    const transform=await page.locator('#as-world').evaluate(e=>e.style.transform);await action('zoom-in').click();assert.notEqual(await page.locator('#as-world').evaluate(e=>e.style.transform),transform);await action('fit').click();
    const canvas=await page.locator('#as-canvas').boundingBox();const beforePan=await page.locator('#as-world').evaluate(e=>e.style.transform);
    await page.mouse.move(canvas.x+12,canvas.y+15);await page.mouse.down();await page.mouse.move(canvas.x+62,canvas.y+45);await page.mouse.up();assert.notEqual(await page.locator('#as-world').evaluate(e=>e.style.transform),beforePan);await action('fit').click();
    await page.locator('#toast-region').evaluate(e=>e.replaceChildren());
    await page.locator(`.as-node[data-id="${A}"]`).click();await page.screenshot({path:path.join(output,'agent-sessions-desktop.png'),fullPage:true});
    // Existing resource CRUD/run/stop and schedule history continue to work.
    await page.locator('[data-tab="scripts"]').click();await page.getByRole('button',{name:'+ New script',exact:true}).click();await page.locator('#f-alias').fill('browser-smoke');await page.locator('#f-command').fill("printf 'flow-browser-ok\\n'");await page.locator('.modal-actions .save').click();
    const row=page.locator('#tbl-scripts tr').filter({hasText:'browser-smoke'});await row.locator('[data-action="run"]').click();await page.locator('.log-pane').filter({hasText:'flow-browser-ok'}).waitFor();
    await row.locator('[data-action="edit"]').click();await page.locator('#f-command').fill("printf 'flow-stop-ready\\n'; sleep 20");await page.locator('.modal-actions .save').click();await row.locator('[data-action="run"]').click();await page.locator('.log-pane').filter({hasText:'flow-stop-ready'}).waitFor();await row.locator('[data-action="run"]').click();
    await req('POST','/api/schedules',{name:'smoke-schedule',every:'daily 12:00',kind:'script',target:'browser-smoke',enabled:false});
    await fs.writeFile(path.join(dir,'history.jsonl'),JSON.stringify({name:'smoke-schedule',start:new Date().toISOString(),end:new Date().toISOString(),exit_code:0,duration_sec:1})+'\n');
    await page.locator('#reload-btn').click();await page.locator('[data-tab="schedules"]').click();await page.locator('#tbl-schedule-history').getByText('smoke-schedule').waitFor();
    await page.locator('[data-tab="scripts"]').click();await page.locator('#toast-region').evaluate(e=>e.replaceChildren());await page.screenshot({path:path.join(output,'resources-desktop.png'),fullPage:true});await row.locator('[data-action="delete"]').click();await row.waitFor({state:'detached'});
    await page.locator('[data-tab="agent-sessions"]').click();await page.locator('.as-node').first().waitFor();
    await context.newPage().then(async mobile=>{await mobile.setViewportSize({width:390,height:844});await mobile.goto(base+page.url().slice(base.length));await mobile.locator('.as-node').first().waitFor();await mobile.screenshot({path:path.join(output,'agent-sessions-mobile.png'),fullPage:true});assert(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'mobile document overflow');await mobile.locator('[data-as-action="link"]').click();await mobile.locator('#as-session-input').waitFor();await mobile.keyboard.press('Escape');assert.equal(await mobile.locator('#modal-bg.open').count(),0);await mobile.close();});
    await fs.writeFile(path.join(dir,'codex'),'#!/bin/sh\nexit 1\n');await page.locator('#reload-btn').click();await page.locator('#as-codex-status').filter({hasText:'unavailable'}).waitFor();
    assert(await action('sync').isDisabled());const missing='01a90000-9999-4000-8000-000000000009';await addSession(missing);assert(await action('fork').isDisabled());
    await action('edit-requirement').click();await page.locator('#as-title').fill('离线仍可维护关联');await page.locator('#as-form button[type=submit]').click();await page.locator('#modal-bg').waitFor({state:'hidden'});await page.locator('.as-heading-line h3').filter({hasText:'离线仍可维护关联'}).waitFor();
    assert.deepEqual(errors,[],'browser runtime errors');
    await fs.writeFile(path.join(output,'result.json'),JSON.stringify({ok:true,tests:['TAPD CRUD','local picker preloading','cached picker before HTTP response','no repeated Codex scan','explicit picker refresh','failed refresh retains results','ambiguous prefix','fork persistence','full resume copy','shared requirements','external fork sync','exclusion persistence','unknown fork recovery without duplication','shared local rename','deep tree','collapse/zoom/pan/keyboard','XSS escaping','script CRUD/run/stop','schedule history','mobile','offline editing'],artifacts:output},null,2));
    console.log(JSON.stringify({ok:true,artifacts:output}));
  } finally {if(browser)await browser.close();server.kill('SIGTERM');await new Promise(resolve=>{if(server.exitCode!==null)return resolve();server.once('exit',resolve);setTimeout(()=>{server.kill('SIGKILL');resolve();},3000).unref();});}
})().catch(e=>{console.error(e);process.exitCode=1;});
