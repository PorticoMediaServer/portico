import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopAppLifetime} from '../src/app-lifetime.ts';
test('real app viewers retire before native platform installers, once, including a late original-module import',async()=>{
 const owner=createDesktopAppLifetime(),events:string[]=[];let complete!:(value:{disposeApp():void})=>void;
 owner.retain(()=>{events.push('platform')});owner.retain(()=>{events.push('broker')});
 void owner.importWebApp(()=>new Promise(resolve=>{complete=resolve}));
 const close=owner.dispose(),again=owner.dispose();assert.equal(close,again);assert.equal(owner.signal.aborted,true);assert.equal(events.length,0);
 complete({disposeApp(){events.push('viewers')}});await close;assert.deepEqual(events,['viewers','broker','platform']);
 assert.throws(()=>owner.importWebApp(async()=>({disposeApp(){}})),{name:'AbortError'});assert.throws(()=>owner.retain(()=>{}),{name:'AbortError'});
});
test('failed original import or disposer still retires every owned native installer without leaking error details',async()=>{
 for(const mode of ['import','dispose']){
  const owner=createDesktopAppLifetime(),events:string[]=[];owner.retain(()=>{events.push('last')});owner.retain(()=>{events.push('failed');throw new Error('/private/never')});
  const imported=owner.importWebApp(async()=>{if(mode==='import')throw new Error('native ID must not surface');return{disposeApp(){throw new Error('viewer error')}}});void imported.catch(()=>{});
  await assert.rejects(owner.dispose(),/^Error: desktop_shutdown_failed$/);assert.deepEqual(events,['failed','last']);
 }
});

test('headless window host joins its actual native restoration without mounting caption markup',async()=>{
 const {createDesktopNativeWindowHost}=await import('../src/window-native-host.ts');
 const listeners=new Map<string,EventListenerOrEventListenerObject>();let acknowledge!:()=>void;
 const writes:unknown[]=[];
 const page={addEventListener(type:string,listener:EventListenerOrEventListenerObject){listeners.set(type,listener)},removeEventListener(type:string){listeners.delete(type)}} as unknown as Document;
 const host=createDesktopNativeWindowHost({page,onEnvironment(){},bridge:{async read(){return{os:'windows',fullscreen:false,maximized:false,focused:true,captionHover:0,captions:{minimize:'Minimize',maximize:'Maximize',restore:'Restore',close:'Close'}}},async action(){},async chrome(value){writes.push(value);if(value.enabled)await new Promise<void>(resolve=>{acknowledge=resolve})}}});
 await new Promise(resolve=>setImmediate(resolve));
 const first=host.dispose(),second=host.dispose();assert.equal(first,second);assert.equal(listeners.size,0);assert.equal(writes.length,1);
 acknowledge();await first;assert.deepEqual(writes.at(-1),{enabled:false,surface:'pages',controlsVisible:true,fullscreen:false,captions:null});
});

test('injected caption controls render, follow native snapshots and report geometry, then retire before native chrome restoration',async()=>{
 const {createDesktopNativeWindowHost}=await import('../src/window-native-host.ts');
 const events:string[]=[],writes:{enabled:boolean;captions:unknown}[]=[],actions:string[]=[],render=Symbol('captions');
 let ports!:Parameters<NonNullable<Parameters<typeof createDesktopNativeWindowHost>[0]['captions']>>[0],created=0;
 const rect={x:1,y:2,width:3,height:4},rects={minimize:rect,maximize:rect,close:rect};
 const page={addEventListener(){},removeEventListener(){}} as unknown as Document;
 const host=createDesktopNativeWindowHost({page,onEnvironment(){},captions(value){created++;ports=value;return{render:render as never,update(native,state){events.push(`update:${native.os}:${state.surface}`)},dispose(){events.push('dispose');value.geometry(null)}}},
  bridge:{async read(){return{os:'windows',fullscreen:false,maximized:false,focused:true,captionHover:0,captions:{minimize:'Minimize',maximize:'Maximize',restore:'Restore',close:'Close'}}},async action(value){actions.push(value)},async chrome(value){events.push(`chrome:${value.enabled}`);writes.push(value)}}});
 assert.equal(created,1);assert.equal(host.render,render);
 await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(events.slice(0,2),['update:windows:pages','chrome:true']);
 ports.geometry(rects);await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(writes.at(-1),{enabled:true,surface:'pages',controlsVisible:true,fullscreen:false,captions:rects});
 await ports.action('minimize');assert.deepEqual(actions,['minimize']);
 events.length=0;const close=host.dispose();assert.equal(host.dispose(),close);await close;
 // Retiring controls clears their geometry first; the controller then restores native chrome.
 assert.deepEqual(events,['dispose','chrome:true','chrome:false']);
 assert.deepEqual(writes.slice(-2),[{enabled:true,surface:'pages',controlsVisible:true,fullscreen:false,captions:null},{enabled:false,surface:'pages',controlsVisible:true,fullscreen:false,captions:null}]);
});
