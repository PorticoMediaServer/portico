import test from 'node:test';
import assert from 'node:assert/strict';
import {AppLog, APP_LOG_BYTES, APP_LOG_AGE_MS, appLog, appLogFetch} from '../src/app-log.ts';

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
