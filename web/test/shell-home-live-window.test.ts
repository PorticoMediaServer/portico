import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {HomeAuthorityRetired, HomeService} from '@core/home-service.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {createShellHomeDocument} from '../src/shell/home-freshness.ts';
import {quietClock} from './settings-quiet-clock.ts';
import {homeChannelSelection, homeEntrySelection} from '@core/home-selection.ts';
import {transformTsx} from './transform-tsx.mjs';
import type {HomeBindings} from '../src/screens/home/Home';
import type {ShellAssemblyContext} from '../src/shell/bootstrap';

// The actual Home component functions, effects and selection facade run here.
// Only browser sensors, the freshness hook adapter and leaf kit renderers are
// controlled. This is lifecycle/source proof, not React DOM or visual evidence.
const key = '__porticoHomeLiveWindowRegression';
const bridge: any = {frame: null, quiet: null, width: 1440, router: {state: {location: {pathname: '/', href: '/'}}, subscribe: () => () => {}, navigate() {}}};
(globalThis as any)[key] = bridge;
const reactModule = import.meta.resolve('react');
// Bind the real React namespace (Fragment, elements, every other export) and override only the
// lifecycle hooks this frame owns, so the actual screen's effect semantics are the frame's.
const hooks = 'data:text/javascript,' + encodeURIComponent(`import * as real from ${JSON.stringify(reactModule)};export * from ${JSON.stringify(reactModule)};const b=globalThis.${key};${['useRef','useMemo','useCallback','useState','useEffect','useSyncExternalStore'].map(name => `export const ${name}=(...args)=>b.frame.${name}(...args);`).join('')}`);
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get:(_,key)=>String(key)});');
  if (url.endsWith('/src/router.tsx')) return module(`export const router=globalThis.${key}.router;`);
  if (url.endsWith('/shell/viewport.ts')) return module(`export const useShellWidth=()=>globalThis.${key}.width;`);
  if (url.endsWith('/platform/freshness.ts')) return module(`export const usePooledService=(_key,make)=>{const service=make();return {service,snapshot:service.getSnapshot()}};export const useFreshness=()=>({hub:globalThis.${key}.hub});export const useRecovering=document=>{const state=globalThis.${key}.frame.useSyncExternalStore(document.subscribe,document.getState);return(state.stale||state.revalidating)&&state.failures<2};`);
  const leaves: Record<string, string[]> = {'layout': ['Hero','HeroFacts','Shelf'], 'artwork': ['Artwork','AmbientArt','ColourBackdrop','GeneratedArt'], 'cards': ['CollectionBanner','LiveCard','PosterCard','RankedCard','SquareCard','WideCard'], 'controls': ['Button','Select','Switch','TextField'], 'states': ['HomeSkeleton','StateLayout','Skeleton'], 'selection': ['SelectionCircle']};
  for (const [area, names] of Object.entries(leaves)) if (url.endsWith(`/kit/${area}/index.ts`)) return module(names.map(name=>`export const ${name}=${JSON.stringify(name)};`).join(''));
  if (url.endsWith('/shell/SelectionSurfaces.tsx')) return module("export const SelectionSurfaces='SelectionSurfaces',SelectionPhoneBar='SelectionPhoneBar';");
  if (url.endsWith('.tsx')) {
    let source = transformTsx(url);
    if (url.endsWith('/screens/home/Home.tsx') || url.endsWith('/screens/browse/PagedShelf.tsx')) source = source.replace(/from (['"])react\1/g, `from ${JSON.stringify(hooks)}`);
    return module(source);
  }
  return next(url, context);
}});
const {HomeScreen} = await import('../src/screens/home/Home.tsx');
const {createShellSelection} = await import('../src/shell/selection.tsx');
test.after(() => {loader.deregister(); delete (globalThis as any)[key];});
const changed = (a?: readonly unknown[], b?: readonly unknown[]) => !a || !b || a.length !== b.length || a.some((v,i)=>!Object.is(v,b[i]));
class Frame {
  slots: any[]=[]; cursor=0; pending: {old?:()=>void; run:()=>void|(()=>void); slot:any}[]=[];
  constructor(readonly owner: Tree, readonly name: string, readonly rowId?: string) {}
  useRef(value: unknown) {return this.slots[this.cursor++] ??= {current:value};}
  useMemo(make:()=>unknown,deps?:readonly unknown[]) {const i=this.cursor++; if(!this.slots[i]||changed(this.slots[i].deps,deps)) this.slots[i]={value:make(),deps};return this.slots[i].value;}
  useCallback(fn:unknown,deps?:readonly unknown[]) {return this.useMemo(()=>fn,deps);}
  useState(initial: any) {const i=this.cursor++;this.slots[i]??={value:typeof initial==='function'?initial():initial};return [this.slots[i].value,(next:any)=>{const value=typeof next==='function'?next(this.slots[i].value):next;if(!Object.is(value,this.slots[i].value)){this.slots[i].value=value;this.owner.dirty=true;}}];}
  useSyncExternalStore(_subscribe:unknown,read:()=>unknown) {this.cursor++;return read();}
  useEffect(run:()=>void|(()=>void),deps?:readonly unknown[]) {const i=this.cursor++,before=this.slots[i];if(!before||changed(before.deps,deps)){const slot={deps,cleanup:undefined};this.slots[i]=slot;this.pending.push({old:before?.cleanup,run,slot});}}
  unmount() {for(const slot of this.slots) slot?.cleanup?.();}
}
class Tree {
  frames=new Map<string,Frame>(); leaves: {type:string;props:any}[]=[]; dirty=false;
  constructor(readonly bindings:HomeBindings) {}
  render() {
    for(let attempt=0;attempt<12;attempt++) {
      this.dirty=false;this.leaves=[];const seen=new Set<string>();
      const visit=(node:ReactNode,path:string,parent?:Frame):void=>{Children.forEach(node,(child,index)=>{
        if(!isValidElement<any>(child))return;const at=path+'/'+(child.key??index);
        if(typeof child.type==='function') {
          let frame=this.frames.get(at);if(!frame){frame=new Frame(this,child.type.name,child.props.row?.id);this.frames.set(at,frame);}seen.add(at);frame.cursor=0;frame.pending=[];bridge.frame=frame;
          visit((child.type as any)(child.props),at,frame);
        } else {
          if(child.props.ref&&typeof child.props.ref==='object') child.props.ref.current??={frame:parent};
          this.leaves.push({type:String(child.type),props:child.props});
          visit(child.props.children,at,parent);
          if(child.type==='Shelf') {child.props.items.forEach((item:any,i:number)=>visit(child.props.renderItem(item,{index:i,visible:true}),at+'/item'+i,parent));if(child.props.items.length)child.props.onVisibleRange?.(0,child.props.items.length-1);}
        }
      });};
      // Enter the genuine exported screen and recursively mount its private rows/cards.
      let frame=this.frames.get('screen');if(!frame){frame=new Frame(this,'HomeScreen');this.frames.set('screen',frame);}seen.add('screen');frame.cursor=0;frame.pending=[];bridge.frame=frame;visit(HomeScreen(this.bindings),'screen',frame);
      for(const [path,old] of this.frames)if(!seen.has(path)){old.unmount();this.frames.delete(path);}
      const pending=[...this.frames.values()].flatMap(f=>f.pending);pending.forEach(p=>p.old?.());pending.forEach(p=>{p.slot.cleanup=p.run();});
      if(!this.dirty)return;
    }
    throw new Error('Home did not settle');
  }
  live() {return this.leaves.filter(n=>n.type==='LiveCard');}
  unmount() {for(const frame of this.frames.values())frame.unmount();this.frames.clear();}
}
const start=Date.parse('2026-10-22T12:00:10Z');
function response(now=start, identity='channel', fence='viewer') {
  const row=(id:string,extra:any)=>({id,viewerFence:fence,title:id,kind:'recent',artworkShape:'poster',entries:[],total:1,start:0,limit:20,hasMore:false,nextCursor:'',revision:{catalog:1,viewer:1},...extra});
  return {serverId:'server',viewerFence:fence,spotlight:[],layout:{revision:1,rowOrder:[],hiddenRowIds:[]},revision:{catalog:1,viewer:1},generatedAt:new Date(now).toISOString(),rows:[
    row('recent',{entries:[{id:'movie',kind:'movie',title:'Actual movie',libraryId:'movies',available:true,navigation:{view:'item',entityId:'movie'},playback:{itemId:'movie'},canSelect:true}]}),
    row('on_now',{artworkShape:'landscape',live:{revision:'live-'+identity,observedAt:new Date(now-1000).toISOString(),expiresAt:new Date(now+10000).toISOString(),sort:'favorites',group:'',groups:[],channels:[{source:{id:'source',name:'Actual source',generation:'1',publishedAt:'2026-10-22T00:00:00Z',availableStart:'',availableEnd:'',provenance:'live-source',refreshState:''},actions:{play:true,favorite:true},channel:{id:identity,sourceId:'source',provenance:'live-source',name:identity,number:'1',group:'',generation:'1',tuneAvailable:true,recordAvailable:false,favorite:true,hidden:false,preferenceRevision:1,mark:{text:'C',seed:identity},programmes:[]}}]}}),
  ]};
}
async function fixture(t:import('node:test').TestContext) {
  let now=start, current=true, serial=0, next:any=response(); const reads:{resolve:(value:unknown)=>void;reject:(error:unknown)=>void;signal?:AbortSignal}[]=[], observers=new Set<any>(), timers=new Map<number,()=>void>(), browser=new EventTarget(), page=Object.assign(new EventTarget(),{visibilityState:'visible',hidden:false});
  const originals=Object.fromEntries(['window','document','IntersectionObserver','devicePixelRatio','setTimeout','clearTimeout'].map(name=>[name,(globalThis as any)[name]]));const realNow=Date.now;Date.now=()=>now;
  Object.assign(globalThis,{window:Object.assign(browser,{innerWidth:1440,innerHeight:1000,scrollY:0}),document:page,devicePixelRatio:1,setTimeout:(fn:()=>void)=>{timers.set(++serial,fn);return serial;},clearTimeout:(id:number)=>timers.delete(id),IntersectionObserver:class {target:any;constructor(readonly notify:any){observers.add(this);}observe(target:any){this.target=target;}disconnect(){observers.delete(this);}}});
  const api:any={requestMetadataUpload:()=>{assert.fail('Home Live selection must not upload metadata');},request:async(_path:string,_method:string,_body:unknown,signal?:AbortSignal)=>next==='pending'?new Promise((resolve,reject)=>reads.push({resolve,reject,signal})):structuredClone(next)};
  const service=new HomeService(api,{observation:{serverId:'server',isCurrent:()=>current,observe:()=> 'accept',retire:()=>{current=false;},mismatch:()=>{}}});await service.load();assert.equal(service.getSnapshot().phase,'ready');
  const errors:unknown[]=[],played:unknown[]=[];
  // Observe the original rejection entering the canonical document; rethrow so debt remains owed.
  const refresh=service.refresh.bind(service);service.refresh=async input=>{try{return await refresh(input);}catch(error){errors.push(error);throw error;}};
  const clock=quietClock(),hub=new FreshnessHub({...clock.env,gatherMs:0,settleMs:0,backoffMs:[1000]}),display=createShellHomeDocument(hub,service);bridge.hub=hub;
  const context={home:service,api,session:()=>({viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}}),events:{on:()=>()=>{}},isCurrent:()=>current} as unknown as ShellAssemblyContext;
  const facade=createShellSelection(context,{play:async()=>{},enqueue:async()=>{},playChannel:async()=>{}},e=>errors.push(e),()=>false);
  const tree=new Tree({service,displayDocument:display.document,selection:facade.selection,artwork:{foreground:()=>undefined,ambient:()=>undefined} as any,time:{locale:'en-US',timeZone:'UTC'},reducedMotion:true,effects:{reportError:e=>errors.push(e),play:async()=>{},playChannel:async c=>{played.push(c);},openEntry(){},openSeeAll(){},openMore(){},setMyList:async()=>{}}});
  const intersect=(id:string,visible:boolean)=>{for(const observer of [...observers])if(observer.target?.frame?.rowId===id)observer.notify([{isIntersecting:visible}]);};
  t.after(()=>{tree.unmount();display.dispose();hub.dispose();facade.dispose();service.dispose();Object.assign(globalThis,originals);Date.now=realNow;});
  return {service,facade,tree,errors,played,reads,timers,page,display:display.document,flushDisplay:(ms=0)=>clock.advance(ms),setTime:(value:number)=>{now=value;},setNext:(value:any)=>{next=value;},retire:()=>{current=false;facade.dispose();service.dispose();},intersect,resize:()=>{(window as any).innerHeight=3307;browser.dispatchEvent(new Event('resize'));intersect('on_now',true);}};
}
async function settle() {for(let i=0;i<12;i++)await Promise.resolve();}

test('actual Home: fresh mount, offscreen expiry, tall resize, fenced canonical refresh, fresh Live admission',async t=>{
  const f=await fixture(t);f.tree.render();assert.equal(f.tree.live().length,1);f.intersect('recent',true);f.tree.render();
  const ordinary=f.service.getSnapshot().document!.rows[0],entry=ordinary.entries[0];assert.ok(f.facade.selection.card(ordinary,entry));
  const stale=f.service.getSnapshot().document!.rows[1],oldAction=f.tree.live()[0].props.onPlay;
  f.setNext('pending');f.setTime(start+10000);f.resize();await f.flushDisplay();
  assert.doesNotThrow(()=>f.tree.render(),'expired visibility must never throw through Home into a Boundary');
  assert.equal(f.tree.live().length,0,'no expired Live action is rendered');assert.deepEqual(f.errors,[]);
  await oldAction();assert.deepEqual(f.played,[],'a retained expired Play callback cannot tune');
  assert.equal(f.facade.selection.channel(stale,stale.live!.channels[0]),undefined);
  assert.equal(f.facade.selection.registerWindow(stale,1,0,0),undefined);
  assert.ok(f.reads.length>0,'visibility requests real HomeService revalidation');assert.equal(f.reads.filter(r=>!r.signal?.aborted).length,1,'expiry/resize/visibility share the current read');
  f.reads.find(r=>!r.signal?.aborted)!.resolve(response(start+10000,'fresh-channel'));await settle();f.tree.render();
  const fresh=f.service.getSnapshot().document!.rows[1];assert.equal(f.tree.live()[0].props.channel.id,'fresh-channel');assert.ok(f.facade.selection.channel(fresh,fresh.live!.channels[0]));
  assert.equal(f.facade.selection.registerWindow(stale,1,0,0),undefined,'old row identity stays rejected after refresh');
  await f.tree.live()[0].props.onPlay();assert.equal(f.played.length,1);assert.deepEqual(f.errors,[]);
  const item=homeChannelSelection(fresh.live!.channels[0],{platform:'web',playbackAvailable:true,queueAvailable:true});assert.equal(f.facade.controller.toggle('home:on_now',f.facade.controller.key(item)).ok,true);
  assert.ok(f.facade.selection.card(ordinary,entry),'unchanged ordinary row stays selectable');
});

test('actual Home refresh: a competing explicit load wins and an older response cannot resurrect its row',async t=>{
  const f=await fixture(t);f.tree.render();f.setNext('pending');f.setTime(start+10000);f.resize();await f.flushDisplay();f.tree.render();assert.ok(f.reads.length);
  const old=f.reads[0];f.setNext(response(start+10000,'new-document'));await f.service.load();f.tree.render();
  const current=f.service.getSnapshot().document!;old.resolve(response(start+10000,'late-old'));await settle();f.tree.render();
  assert.equal(f.service.getSnapshot().document,current);assert.equal(current.viewerFence,'viewer');assert.equal(current.rows[1].live!.channels[0].channel.id,'new-document');assert.equal(old.signal?.aborted,true);
});

test('an actual changed Home fence retires the observed owner instead of adopting a successor document',async t=>{
  const f=await fixture(t);f.tree.render();const shown=f.service.getSnapshot().document!,row=shown.rows[1],oldAction=f.tree.live()[0].props.onPlay;
  f.setNext(response(start,'foreign-channel','other-viewer'));
  await assert.rejects(f.service.load(),HomeAuthorityRetired);
  assert.equal(f.service.getSnapshot().document,shown);assert.equal(f.facade.selection.channel(row,row.live!.channels[0]),undefined);
  await oldAction();assert.deepEqual(f.played,[]);
});

test('actual Home refresh: unmount/current authority retirement aborts the old read and rejects all old registrations',async t=>{
  const f=await fixture(t);f.tree.render();const oldRow=f.service.getSnapshot().document!.rows[1];f.setNext('pending');f.setTime(start+10000);f.resize();await f.flushDisplay();f.tree.render();assert.ok(f.reads.length);
  const shown=f.service.getSnapshot().document;f.tree.unmount();f.reads.forEach(r=>r.resolve(response(start+10000,'late')));await settle();assert.ok(f.reads.every(r=>r.signal?.aborted));assert.equal(f.service.getSnapshot().document,shown,'an unmounted screen cannot publish its late refresh');f.retire();
  assert.equal(f.facade.selection.registerWindow(oldRow,1,0,0),undefined);assert.equal(f.facade.selection.channel(oldRow,oldRow.live!.channels[0]),undefined);
});

test('controller admission still rejects actual expiry, wrong row identity and old context; facade does not advertise them',async t=>{
  const f=await fixture(t),row=f.service.getSnapshot().document!.rows[1];assert.throws(()=>f.facade.controller.registerWindow({id:'copy',row:{...row},sectionOrder:1}),/unoffered-home-row/);
  const cleanup=f.facade.selection.registerWindow(row,1,0,0);assert.equal(typeof cleanup,'function');f.setTime(start+10000);
  assert.deepEqual(f.facade.controller.registerWindow({id:'expired',row,sectionOrder:1}),{ok:false,reason:'stale-home-live-window'});assert.equal(f.facade.selection.registerWindow(row,1,0,0),undefined);cleanup?.();
});

test('actual Home: failed expired-window refresh leaves no stale actions and foreground retries through the service',async t=>{
  const f=await fixture(t);f.tree.render();f.setNext('pending');f.setTime(start+10000);f.resize();await f.flushDisplay();f.tree.render();
  const error=Object.assign(new Error('unavailable'),{status:503});f.reads[0].reject(error);await settle();f.tree.render();
  assert.equal(f.tree.live().length,0);assert.ok(f.errors.length);assert.ok(f.errors.every((error:any)=>error.status===503));assert.equal(f.errors[0],error);assert.equal(f.display.getState().failures,1);assert.equal(f.display.getState().stale,true);
  f.setNext(response(start+10000,'recovered'));f.page.dispatchEvent(new Event('visibilitychange'));await f.flushDisplay(1000);await settle();f.tree.render();
  assert.equal(f.tree.live()[0].props.channel.id,'recovered');
});

test('expiry clears an expired Live selection while keeping a genuine ordinary selection usable',async t=>{
  const f=await fixture(t);f.tree.render();f.intersect('recent',true);f.intersect('on_now',true);f.tree.render();
  const document=f.service.getSnapshot().document!,ordinary=document.rows[0],live=document.rows[1];
  const channel=f.facade.selection.channel(live,live.live!.channels[0])!;channel.onToggle({range:false});assert.equal(f.facade.controller.getSnapshot().selection.active,true);
  f.setNext('pending');f.setTime(start+10000);f.resize();await f.flushDisplay();f.tree.render();assert.equal(f.facade.controller.getSnapshot().selection.active,false);
  const entry=homeEntrySelection(ordinary.entries[0],{platform:'web',playbackAvailable:true,queueAvailable:true})!;
  assert.equal(f.facade.controller.toggle('home:recent',f.facade.controller.key(entry)).ok,true);
  assert.equal(f.facade.controller.getSnapshot().selection.active,true);assert.equal(f.facade.selection.registerWindow(live,1,0,0),undefined);
  assert.equal(f.facade.controller.getSnapshot().selection.active,true,'an expired unselected Live row cannot clear ordinary selection');
});

test('expiry between the facade and controller clock checks still creates no window',async t=>{
  const f=await fixture(t),row=f.service.getSnapshot().document!.rows[1],clock=Date.now;let reads=0;
  Date.now=()=>++reads===1?start+9999:start+10000;
  try {assert.equal(f.facade.selection.registerWindow(row,1,0,0),undefined);assert.deepEqual(f.errors,[]);}
  finally {Date.now=clock;}
  const item=homeChannelSelection(row.live!.channels[0],{platform:'web',playbackAvailable:true,queueAvailable:true});
  assert.equal(f.facade.controller.toggle('home:on_now',f.facade.controller.key(item)).ok,false,'no refused window was registered');
});
