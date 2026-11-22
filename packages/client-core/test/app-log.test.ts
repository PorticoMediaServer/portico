import test from 'node:test';
import assert from 'node:assert/strict';
import {AppLog, APP_LOG_BYTES, APP_LOG_AGE_MS, appLog, appLogFetch, appLogShownError} from '../src/app-log.ts';

test('write-time allowlist drops every forbidden class from memory and restored records', () => {
 const log = new AppLog(()=>100);
 const forbidden = [
  'password=sample-password','PIN=1234','two-step=234567','recovery=sample-recovery','setup=sample-setup','pairing=sample-pair',
  'move=sample-move','invitation=sample-invite','https://web.example/join#sample-invite','Portico sign-in note sample-note',
  'apiKey=sample-api-key','Bearer sample-token','Cookie=sample-cookie','Authorization=sample-auth',
  'https://server.example/v1/media/path-grant/subtitles/segment','/v1/media/path-grant/pages/0','192.0.2.44','server.example',
  '/home/person/private/file','C:\\Users\\Person\\file','what somebody typed','Other Person',
 ];
 for (const secret of forbidden) {
  log.write('error','app','error',{message:secret,url:secret,headers:secret,password:secret,code:secret,screen:secret,itemId:secret,profileId:secret,appVersion:secret});
  log.restore(JSON.stringify({time:100,level:'error',area:'app',message:'error',facts:{code:secret,url:secret}}));
 }
 assert.deepEqual(JSON.parse(log.snapshot().split('\n')[0]).facts,{});
 for (const secret of forbidden) assert.ok(!log.snapshot().includes(secret),secret);
 log.write('info','playback','playback',{itemId:'itm_0123456789abcdef',profileId:'prf_0123456789abcdef',route:'converted',codec:'aac',engine:'mpv',playerErrorCode:-12909,appVersion:'1.4.0'});
 const allowed = JSON.parse(log.snapshot().split('\n').at(-1)!);
 assert.equal(allowed.facts.itemId,'itm_0123456789abcdef'); assert.equal(allowed.facts.playerErrorCode,-12909);
});

test('the ring never exceeds one MiB and drops lines older than seven days on read, write and restore', () => {
 let now = 100; const log = new AppLog(()=>now);
 for (let i=0;i<18000;i++) {now++;log.write('info','app','started',{appVersion:'1.4.0'});}
 assert.ok(new TextEncoder().encode(log.snapshot()).length <= APP_LOG_BYTES);
 assert.ok(!log.snapshot().includes('"time":101,'));
 const saved = log.snapshot(); now += APP_LOG_AGE_MS + 1;
 assert.equal(log.snapshot(),''); log.restore(saved); assert.equal(log.snapshot(),'');
 log.write('error','app','crash'); assert.ok(log.snapshot());log.clear(); assert.equal(log.snapshot(),'');
});

test('network capture records status and request id while preserving the response and never reading addresses or bodies', async () => {
 appLog.clear();
 const response = new Response('private response body',{status:503,headers:{'X-Request-ID':'req_0123456789abcdef'}});
 const fetcher = appLogFetch(async()=>response);
 assert.equal(await fetcher('https://private.example/v1/media/secret-path',{headers:{Authorization:'Bearer private-token'},body:'typed words'}),response);
 const line=JSON.parse(appLog.snapshot());assert.equal(line.facts.status,503);assert.equal(line.facts.requestId,'req_0123456789abcdef');
 assert.equal(line.facts.code,'http_error');assert.ok(!appLog.snapshot().includes('private'));assert.equal(await response.text(),'private response body');appLog.clear();
});

test('a failure request is logged with its method, and a shown error with where it came from, its code and status', async () => {
 const {presentError} = await import('../src/presentation/errors.ts');
 appLog.clear();
 const fetcher = appLogFetch(async()=>new Response('',{status:503,headers:{'X-Request-ID':'req_0123456789abcdef'}}));
 await fetcher('https://private.example/v1/sessions/ses_0123456789abcdef/progress',{method:'post'});
 assert.equal(JSON.parse(appLog.snapshot()).facts.method,'POST');
 appLog.clear();
 // "Your server is busy right now" after the player (finisher, 21 Nov): the line names the page and the code.
 const busy = Object.assign(new Error('Server busy at https://private.example/v1/x'),{code:'server_busy',status:503});
 presentError(busy,'detail',{operation:'action'}); presentError(busy,'detail',{operation:'action'});
 const lines = appLog.snapshot().split('\n');
 assert.equal(lines.length,1,'a re-rendered error is written once');
 assert.deepEqual(JSON.parse(lines[0]).facts,{context:'detail',operation:'action',category:'busy',errorCode:'server_busy',status:503});
 assert.ok(!appLog.snapshot().includes('private.example'));
});

test('only code-shaped error codes and known contexts enter a shown-error line', () => {
 const log = new AppLog(()=>100);
 for (const bad of ['what somebody typed','Bearer sample-token','server.example','/v1/media/path-grant','Other Person','password=sample','a'.repeat(60),'sample@example.com'])
  log.write('warn','app','error',{errorCode:bad,context:bad,operation:bad,category:bad,method:bad});
 for (const line of log.snapshot().split('\n')) assert.deepEqual(JSON.parse(line).facts,{});
});

test('two errors alternating on one page write one line each, not one per render', async () => {
 const {presentError} = await import('../src/presentation/errors.ts');
 appLog.clear();
 const busy = Object.assign(new Error('x'), {code: 'server_busy', status: 503}), gone = Object.assign(new Error('y'), {code: 'not_found', status: 404});
 for (let n = 0; n < 6; n++) {presentError(busy, 'search', {operation: 'save'}); presentError(gone, 'saved');}
 assert.equal(appLog.snapshot().split('\n').length, 2);
});

test('a code the client does not know is logged as other: a name, a PIN or an invitation code never enters the log', async () => {
 const {presentError} = await import('../src/presentation/errors.ts');
 appLog.clear();
 for (const code of ['mary-smith', 'pin-1234', 'wxyz-k3m9-p2qr-st45']) presentError(code, 'account');
 presentError(Object.assign(new Error('z'), {code: 'some_server_thing', status: 500}), 'home');
 const lines = appLog.snapshot().split('\n').map(line => JSON.parse(line).facts);
 assert.ok(lines.every(facts => facts.errorCode === 'other'), JSON.stringify(lines));
 for (const secret of ['mary-smith', 'pin-1234', 'wxyz', 'some_server_thing']) assert.ok(!appLog.snapshot().includes(secret), secret);
});

test('the playback copy writes its line too, with the category playback', async () => {
 const {presentError} = await import('../src/presentation/errors.ts');
 appLog.clear();
 presentError(Object.assign(new Error('p'), {code: 'timeout', status: 504}), 'playback');
 assert.deepEqual(JSON.parse(appLog.snapshot()).facts, {context: 'playback', operation: 'load', category: 'playback', errorCode: 'timeout', status: 504});
});

test('the write-time allowlist itself keeps only known error codes: any writer and an old restored line alike', async () => {
 await import('../src/presentation/errors.ts');
 const log = new AppLog(()=>100);
 for (const code of ['mary-smith', 'pin-1234', 'constructor', 'some_server_thing']) {
  log.write('warn','app','error',{errorCode: code});
  log.restore(JSON.stringify({time:100,level:'warn',area:'app',message:'error',facts:{errorCode: code}}));
 }
 for (const line of log.snapshot().split('\n')) assert.equal(JSON.parse(line).facts.errorCode, undefined);
 log.write('warn','app','error',{errorCode: 'server_busy'}); log.write('warn','app','error',{errorCode: 'other'});
 assert.deepEqual(log.snapshot().split('\n').slice(-2).map(line => JSON.parse(line).facts.errorCode), ['server_busy', 'other']);
});

test('a failure kept on screen writes one line however it is re-rendered, and writes again after five quiet seconds', () => {
 appLog.clear();
 const facts = {context: 'person', operation: 'load', category: 'not-found', errorCode: 'not_found', status: 404} as const;
 // A page that builds a fresh error every render, every second for a minute: still one line.
 for (let second = 0; second <= 60; second++) appLogShownError({...facts}, 1_000_000 + second * 1000);
 assert.equal(appLog.snapshot().split('\n').length, 1, 'one line while it stays on screen');
 // Five quiet seconds, then the same failure again: a new line (it is a new occurrence, not forgotten for the session).
 appLogShownError({...facts}, 1_000_000 + 66_000);
 assert.equal(appLog.snapshot().split('\n').length, 2);
 // A different failure is its own line at once.
 appLogShownError({...facts, category: 'changed', errorCode: 'other', status: 412}, 1_000_000 + 66_500);
 assert.equal(appLog.snapshot().split('\n').length, 3);
});

test('a fact-less line repeated within a minute is kept once, so a warning flood cannot push useful lines out', () => {
 let now = 1_000_000; const log = new AppLog(()=>now);
 log.write('info','playback','playback',{state:'playing',engine:'native'});
 for (let i = 0; i < 5000; i++) { now += 500; log.write('warn','app','warning'); }
 const lines = log.snapshot().split('\n').map(line=>JSON.parse(line));
 assert.equal(lines.filter(line=>line.message==='playback').length, 1, 'the useful line survives the flood');
 // 5000 warnings over 2,500 s: one kept a minute.
 const warnings = lines.filter(line=>line.message==='warning');
 assert.ok(warnings.length >= 41 && warnings.length <= 43, `kept ${warnings.length} warning lines`);
});

test('lines that carry facts are never folded, and a fact-less line returns after a quiet minute', () => {
 let now = 1_000_000; const log = new AppLog(()=>now);
 log.write('warn','network','network-failure',{method:'GET',status:503,code:'http_error'});
 log.write('warn','network','network-failure',{method:'GET',status:503,code:'http_error'});
 log.write('warn','app','warning'); now += 59_999; log.write('warn','app','warning'); now += 1; log.write('warn','app','warning');
 log.write('error','app','error');
 const lines = log.snapshot().split('\n').map(line=>JSON.parse(line));
 assert.equal(lines.filter(line=>line.message==='network-failure').length, 2);
 assert.equal(lines.filter(line=>line.message==='warning').length, 2);
 assert.equal(lines.filter(line=>line.level==='error').length, 1, 'a different level is its own key');
});
