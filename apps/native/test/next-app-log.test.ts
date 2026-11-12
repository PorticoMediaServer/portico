import test from 'node:test';
import assert from 'node:assert/strict';
import {AppLog, APP_LOG_AGE_MS} from '../../../packages/client-core/src/app-log';
import {createManualScheduler} from '../../../packages/client-core/src/timer';
import {screenModule} from './helpers/screen-harness.mjs';

async function platform(read: () => Promise<string | null>) {
 const log = new AppLog(), scheduler = createManualScheduler(), writes: (string | null)[] = [];
 let background: ((state:string)=>void) | undefined;
 const modules = {react:{default:{}},
  'react-native': {NativeModules:{PorticoRuntime:{loadAppLog:read,saveAppLog:async(text:string|null)=>{writes.push(text);}},PorticoCapabilities:{facts:async()=>({appVersion:'1.4.0'})}},Platform:{Version:'17.0'},AppState:{addEventListener:(_event:string,fn:(state:string)=>void)=>{background=fn;}}},
  '../../../../packages/client-core/src/app-log': {appLog:log},
  '../../../../packages/client-core/src/timer': {systemScheduler:scheduler},
 };
 const bridge = await screenModule(new URL('../src-next/platform/app-log.ts',import.meta.url),modules);
 return {log,scheduler,writes,bridge,background:()=>background?.('background')};
}
test('native restores its last-run ring, expires old lines, flushes on background and clears durable storage',async()=>{
 const old=new AppLog(()=>Date.now()-APP_LOG_AGE_MS-1000);old.write('info','app','started');
 const kept=new AppLog(()=>Date.now()-1000);kept.write('warn','app','warning');
 const p=await platform(async()=>old.snapshot()+'\n'+kept.snapshot());
 p.bridge.installNativeAppLog();await p.bridge.flushNativeAppLog();
 assert.equal(p.log.snapshot().split('\n').filter(line=>line.includes('"warning"')).length,1);
 assert.ok(!p.log.snapshot().includes(old.snapshot()));
 p.log.write('error','app','error');p.background();await p.bridge.flushNativeAppLog();assert.ok(p.writes.at(-1)?.includes('"error"'));
 await p.bridge.clearNativeAppLog();assert.equal(p.log.snapshot(),'');assert.equal(p.writes.at(-1),null);assert.equal(p.scheduler.pending(),0);
});
test('a late last-run read cannot restore lines after sign-out cleared the log',async()=>{
 let release!: (text:string)=>void;
 const saved = new Promise<string>(resolve=>{release=resolve;});
 const p=await platform(()=>saved);p.bridge.installNativeAppLog();
 const cleared=p.bridge.clearNativeAppLog();
 release(JSON.stringify({time:Date.now(),level:'error',area:'app',message:'crash',facts:{}}));
 await cleared;assert.equal(p.log.snapshot(),'');assert.equal(p.writes.at(-1),null);
});
