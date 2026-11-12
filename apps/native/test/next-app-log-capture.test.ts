import test from 'node:test';
import assert from 'node:assert/strict';
import {AppLog} from '../../../packages/client-core/src/app-log';
import * as journalTypes from '../src-next/platform/error-journal';
import {screenModule} from './helpers/screen-harness.mjs';

test('native console/crash capture keeps codes only in the shared ring and the saved crash, with no raw exception text',async t=>{
 const log=new AppLog();let saved: unknown,handler: ((error:unknown,fatal?:boolean)=>void)|undefined;
 const original=(globalThis as any).ErrorUtils;
 (globalThis as any).ErrorUtils={getGlobalHandler:()=>()=>{},setGlobalHandler:(fn:typeof handler)=>{handler=fn;}};
 const warn=console.warn,error=console.error;
 t.after(()=>{console.warn=warn;console.error=error;(globalThis as any).ErrorUtils=original;});
 const capture=await screenModule(new URL('../src-next/platform/error-capture.ts',import.meta.url),{
  react:{default:{}},'react-native':{NativeModules:{PorticoCapabilities:{facts:async()=>({appVersion:'1.4.0'})}}},
  '../../../../packages/client-core/src/app-log':{appLog:log,APP_LOG_AGE_MS:7*86400000},
  './app-log':{installNativeAppLog:()=>{},flushNativeAppLog:async()=>{}},'./error-journal':journalTypes,
  './queue-storage':{NativeQueueStore:class{async change(_key:string,update:(v:unknown)=>{record:unknown;value:unknown}){const out=update(saved);saved=out.record;return out.value;}}},
 });
 const secret='https://private.example/v1/media/path-grant/subtitles/secret what was typed';
 capture.journal.note('error',secret);
 handler?.(Object.assign(new TypeError(secret),{stack:secret}),true);
 await new Promise(resolve=>setImmediate(resolve));
 assert.ok(log.snapshot().includes('"crash"'));assert.ok(!log.snapshot().includes(secret));
 assert.ok(!JSON.stringify(saved).includes('private.example'));assert.equal((saved as {stack:string}).stack,'');
 const crash=await capture.lastCrash();assert.equal(crash.message,'Error');assert.equal(crash.stack,'');
});
