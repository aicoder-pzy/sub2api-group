import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import path from 'node:path'
const { chromium } = createRequire(path.resolve(process.env.BROWSER_TEST_MODULES, 'package.json'))('playwright')
const output=path.resolve('pelican-creative-browser-results')
const fsOutput=await import('node:fs/promises')
await fsOutput.mkdir(output,{recursive:true})
const browser=await chromium.launch({headless:true})
const context=await browser.newContext({viewport:{width:1440,height:1100},locale:'zh-CN'})
const page=await context.newPage()
try {
const fs = await import('node:fs/promises');
const pool = JSON.parse(await fs.readFile('backend/internal/service/pelican_creative_materials.json', 'utf8'));
const user = { id: 1, role: 'admin', email: 'fixture@example.invalid', username: 'Admin', balance: 100, status: 'active', concurrency: 5 };
const groups = [
  {id:4,name:'GPT 不降智',platform:'openai',status:'active',subscription_type:'standard',rate_multiplier:1},
  {id:5,name:'Claude Max',platform:'anthropic',status:'active',subscription_type:'standard',rate_multiplier:1}
];
const round = { id: pool.version + ':20261107T0900Z', seed: '174df147e5f7b307c619d5468a9c189243e68f507b9bce650cf3cdf608f20f8e', pool_version:pool.version, started_at:'2026-11-07T09:00:00Z', materials:pool.categories.map(c=>({key:c.key,label:c.label,value:c.values[c.key==='theme'?1:0]})), prompt:'请制作一个自包含的单文件 HTML，以内联 SVG 呈现一段能够自然循环的二维动画故事。\n'+pool.categories.map(c=>c.label+'：'+c.values[0]).join('\n')+'\n主体与元素参与事件，场景影响动作；只输出完整 HTML。', checks:{has_svg:true,animated:true,no_external:true,complete:true,matched:['subject','element1','element2','setting'],missing:[]} };
const artwork = color => `<html><head><style>html,body{margin:0;background:#0c1721;height:100%;display:grid;place-items:center}svg{width:100%;height:100%}@keyframes float{50%{transform:translateY(-14px)}}g{animation:float 3s ease-in-out infinite}</style></head><body><svg viewBox="0 0 800 600" xmlns="http://www.w3.org/2000/svg"><defs><radialGradient id="b"><stop stop-color="${color}" stop-opacity=".55"/><stop offset="1" stop-color="#102731"/></radialGradient></defs><rect width="800" height="600" fill="url(#b)"/><path d="M0 470Q230 380 400 470T800 450V600H0Z" fill="#143a46"/><g><circle cx="400" cy="285" r="100" fill="${color}"/><circle cx="360" cy="260" r="10" fill="#0c1721"/><circle cx="440" cy="260" r="10" fill="#0c1721"/><path d="M365 305Q400 345 435 305" stroke="#0c1721" stroke-width="8" fill="none"/><path d="M310 330L230 400M490 330L570 400" stroke="${color}" stroke-width="28" stroke-linecap="round"/></g><text x="400" y="530" text-anchor="middle" fill="#d5e9e3" font-size="22">轻盈与重量 · 动画验证素材</text></svg></body></html>`;
const items = groups.flatMap((group,i)=>[0,1].map(j=>({id:100+i*10+j,group_id:group.id,model_id:i?'claude-opus-5-5':'gpt-6-astra',reasoning_effort:'medium',latency_ms:54000+i*16000,generated_at:'2026-11-07T09:01:00Z',quality_verdict:'unknown',quality_reason:'',creative_round:j?{...round,id:round.id+'-previous',started_at:'2026-11-07T08:30:00Z'}:round,response_text:artwork(i?'#d69f6f':'#68b6a0')})));
const plans=groups.map((g,i)=>({id:7+i,group_id:g.id,group_name:g.name,group_platform:g.platform,group_status:'active',model_id:i?'claude-opus-5-5':'gpt-6-astra',cron_expression:'*/30 * * * *',enabled:true,today_cost_usd:.12,total_cost_usd:2.34,today_cost_incomplete:false,total_cost_incomplete:false,pelican_config:{question_kind:'pelican',prompt_mode:i?'random_creative':'fixed',prompt:'请绘制一只骑自行车的鹈鹕。',reasoning_effort:'medium',parallel_count:1},last_run_at:'2026-11-07T09:00:00Z',next_run_at:'2026-11-07T09:30:00Z',created_at:'2026-11-07T08:00:00Z',updated_at:'2026-11-07T09:00:00Z'}));
const results=items.map((item,i)=>({id:item.id,plan_id:item.group_id===4?7:8,group_id:item.group_id,group_name:groups.find(g=>g.id===item.group_id).name,account_id:150+i,account_name:'测试账号 '+(150+i),attempts:[],status:'success',response_text:item.response_text,error_message:'',latency_ms:item.latency_ms,cost_usd:.01,cost_incomplete:false,pelican_config:{question_kind:'pelican',prompt_mode:'random_creative',prompt:item.creative_round.prompt,reasoning_effort:item.reasoning_effort,parallel_count:1,model_id:item.model_id,creative_round:item.creative_round},started_at:item.generated_at,finished_at:item.generated_at,created_at:item.generated_at}));
const grade={version:'creative-vision-v1',status:'failed',score:42,confidence:'high',reason:'示例：元素二没有参与动作，场景与故事脱节，动画仅整体平移。',model_id:'用户填写的裁判模型',criteria:[{key:'materials',score:2,reason:'主体可辨认，但元素二缺失。'},{key:'story',score:1,reason:'动作没有与场景建立联系。'},{key:'visual',score:3,reason:'造型清楚，构图基本完整。'},{key:'animation',score:2,reason:'采样帧只有整体平移，没有角色动作。'}],materials:[{key:'subject',status:'present',reason:'主体位于画面中间。'},{key:'element1',status:'present',reason:'主体左侧可见。'},{key:'element2',status:'missing',reason:'三个时刻均未见对应形状。'},{key:'setting',status:'present',reason:'背景符合场景。'}],motion_detected:true,checked_at:'2026-11-07T10:00:00Z'};
for (const item of items) item.creative_round={...item.creative_round,evaluation:{...grade,status:item.id===100?'passed':'failed',score:item.id===100?86:42}};
for (const result of results) { result.pelican_config.creative_round = items.find(item => item.id === result.id).creative_round; result.pelican_config.creative_round.verification = { quality:'degraded',reason:'附加路径计数核验未通过',checked_at:grade.checked_at }; }
globalThis.creativeUI = { requests:[],errors:[],writes:[],items,round };
page.on('pageerror',e=>creativeUI.errors.push(e.message));
await page.addInitScript(user=>{
  if (window.top !== window) return;
  localStorage.setItem('auth_token','LOCAL_CREATIVE_UI_FIXTURE');
  localStorage.setItem('auth_user',JSON.stringify(user));
  localStorage.setItem('sub2api_locale','zh');
  localStorage.setItem('admin_guide_1_admin_v4_interactive','true');
}, user);
await page.route('**/setup/status', route=>route.fulfill({json:{code:0,data:{needs_setup:false}}}));
await page.route('**/api/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),p=url.pathname;
  creativeUI.requests.push({path:p,method:req.method()});
  const json=data=>route.fulfill({json:{code:0,data}});
  if(p.endsWith('/auth/me')) return json(user);
  if(p.endsWith('/settings/public')) return json({site_name:'EasyGPT',backend_mode_enabled:false,custom_menu_items:[],pelican_showcase_enabled:true});
  if(p.includes('/setup/status')) return json({needs_setup:false});
  if(p.endsWith('/admin/compliance')) return json({required:false});
  if(p.endsWith('/admin/groups/all')) return json(groups);
  if(p.endsWith('/pelican-group-tests/creative-materials')) return json({pool,round});
  if(p.endsWith('/pelican-group-tests')) return json(plans);
  if(p.endsWith('/pelican-group-tests/7') && req.method()==='PUT') {
    const form=req.postDataJSON(); creativeUI.writes.push(form);
    plans[0]={...plans[0],...form,pelican_config:{...plans[0].pelican_config,...form}};
    return json(plans[0]);
  }
  if(p.endsWith('/pelican-group-test-results')) return json({items:results,total:results.length,page:1,page_size:20,pages:1});
  if(p.includes('/pelican-group-test-results/')) return json(results.find(r=>r.id===Number(p.split('/').at(-1))));
  if(p.endsWith('/pelican-showcase/settings')) return json({enabled:true,api_enabled:true,max_items:50,auto_cleanup:true,retention_days:7});
  if(p.endsWith('/pelican-showcase')) return json({enabled:true,api_enabled:true,max_items:50,retention_days:7,groups:groups.map(g=>({...g,items:items.filter(i=>i.group_id===g.id).map(({response_text,...i})=>({...i,creative_round:{...i.creative_round,prompt:undefined}}))}))});
  if(p.includes('/pelican-showcase/items/')) return json(items.find(i=>i.id===Number(p.split('/').at(-1))));
  return json([]);
});
await page.setViewportSize({width:1440,height:1100});
await page.goto('http://127.0.0.1:4173/admin/pelican-tests',{waitUntil:'domcontentloaded'});
await page.getByTestId('pelican-plan-edit-7').waitFor();


  const editor=page.getByTestId('pelican-test-editor')
  await page.getByTestId('pelican-plan-edit-7').click()
  await page.getByTestId('pelican-test-mode').selectOption('random_creative')
  await editor.getByTestId('creative-judge-fields').waitFor()
  assert.deepEqual(await editor.locator('#creative-judge-group option').allTextContents(),['选择可信的裁判分组','GPT 不降智'])
  await page.getByTestId('pelican-test-save').click()
  assert.equal(creativeUI.writes.length,0,'enabled random plans require a judge')
  await editor.locator('#creative-judge-group').selectOption('4')
  await editor.locator('#creative-judge-model').fill('trusted-vision-model')
  await page.screenshot({path:path.join(output,'judge-configuration.png')})
  await page.getByTestId('pelican-test-save').click()
  await editor.waitFor({state:'hidden'})
  assert.equal(creativeUI.writes.length,1)
  assert.equal(creativeUI.writes[0].creative_judge_group_id,4)
  assert.equal(creativeUI.writes[0].creative_judge_model_id,'trusted-vision-model')
  await page.getByTestId('pelican-result-view-101').click()
  const preview=page.getByTestId('pelican-result-preview')
  await preview.waitFor()
  await preview.getByTestId('creative-details').locator('summary').first().click()
  assert.match(await preview.getByTestId('creative-score').textContent(),/42\/100.*未达标/)
  assert.match(await preview.getByTestId('creative-evaluation').textContent(),/元素二缺失/)
  assert.match(await preview.getByTestId('creative-verification').textContent(),/未通过/)
  assert.match(await preview.getByTestId('pelican-quality-badge').textContent(),/视觉评分.*42\/100.*未达标/)
  assert.equal(await preview.locator('iframe').getAttribute('sandbox'),'allow-scripts')
  await page.screenshot({path:path.join(output,'scoring-details-desktop.png')})
  await page.setViewportSize({width:390,height:844})
  // AppLayout animates its desktop sidebar margin for 300 ms on resize.
  await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:2000})
  await page.screenshot({path:path.join(output,'scoring-details-phone.png')})
  const dimensions=await page.evaluate(()=>({width:document.documentElement.scrollWidth,viewport:innerWidth}))
  assert.ok(dimensions.width<=dimensions.viewport,'phone layout must not overflow')
  await page.goto('http://127.0.0.1:4173/pelican-showcase',{waitUntil:'domcontentloaded'})
  const card=page.getByTestId('pelican-showcase-card').nth(1)
  await card.getByTestId('pelican-quality-badge').waitFor()
  assert.match(await card.getByTestId('pelican-quality-badge').textContent(),/42\/100.*未达标/)
  await card.getByRole('button').click()
  const galleryPreview=page.getByTestId('showcase-preview')
  await galleryPreview.waitFor()
  assert.match(await galleryPreview.getByTestId('creative-score').textContent(),/42\/100.*未达标/)
  assert.equal(creativeUI.requests.filter(r=>/pelican-assessments|\/run$/.test(r.path)).length,0,'viewing results must not invoke a judge or generate')
  assert.deepEqual(creativeUI.errors,[])
  await fsOutput.writeFile(path.join(output,'verification.json'),JSON.stringify({judge_config_saved:true,unconfigured_save_blocked:true,grade_persisted_in_history_and_gallery:true,reasoning_result_separate:true,phone_no_overflow:true,paid_requests:0},null,2))
} catch(error) {
  await fsOutput.writeFile(path.join(output,'failure-layout.json'),JSON.stringify(await page.evaluate(()=>({
    width:document.documentElement.scrollWidth,viewport:innerWidth,
    elements:[...document.querySelectorAll('body *')].map(el=>({tag:el.tagName,class:el.className,
      left:el.getBoundingClientRect().left,right:el.getBoundingClientRect().right}))
      .filter(el=>el.left<0||el.right>innerWidth).slice(0,40)
  })),null,2)).catch(()=>{})
  await page.screenshot({path:path.join(output,'failure.png'),fullPage:true}).catch(()=>{})
  throw error
} finally { await browser.close() }
