import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeService} from '@core/home-service.ts';
import {FreshnessHub, dependsOn} from '@core/freshness-hub.ts';
import {clock} from './shell-facts-fixture.ts';
import {createShellHomeDocument, HomeReadNotAnswered, watchShellHomePreferences} from '../src/shell/home-freshness.ts';

const manifest = () => ({serverId:'server',viewerFence:'home-viewer',generatedAt:'2026-10-28T12:00:00.000Z',rows:[],spotlight:[],
  revision:{catalog:1,viewer:1},layout:{revision:1,rowOrder:[],hiddenRowIds:[]}});
test('Home borrows one shell registration: clean reopen reads nothing, dirty reopen catches up without disposing service', async () => {
  let reads=0, observations=0;
  const service=new HomeService({request:async<T>()=>{reads++;return manifest() as T;}},{observation:{serverId:'server',isCurrent:()=>true,
    observe:()=>{observations++;return 'accept';},retire:assert.fail,mismatch:assert.fail}});
  const time=clock(),hub=new FreshnessHub({...time.env,gatherMs:0,settleMs:0,random:()=>0}),owner=createShellHomeDocument(hub,service);
  await service.load();const document=service.getSnapshot().document;assert.ok(document);
  owner.document.setOpen(true);owner.document.setOpen(false);owner.document.setOpen(true);await time.advance(0);assert.equal(reads,1);
  owner.document.setOpen(false);hub.note(dependsOn.libraries);await time.advance(0);assert.equal(reads,1);
  await service.authority({isCurrent:()=>true});assert.equal(reads,2);assert.equal(observations,2);
  assert.equal(service.getSnapshot().document,document,'authority-only read never replaces the display');assert.equal(owner.document.getState().stale,true);
  owner.document.setOpen(true);await time.advance(0);assert.equal(reads,3);assert.equal(owner.document.getState().stale,false);
  owner.document.setOpen(false);owner.dispose();await service.load();assert.equal(reads,4,'retiring display registration does not dispose shell service');
  service.dispose();hub.dispose();
});
test('a display refresh that adopted nothing retains debt instead of resolving void as freshness', async () => {
  let reads=0;
  const service={getSnapshot:()=>({phase:'ready',document:manifest()}),subscribe:()=>()=>{},refresh:async()=>{reads++;return 'not-answered';}};
  const time=clock(),hub=new FreshnessHub({...time.env,gatherMs:0,settleMs:0,backoffMs:[60000],random:()=>0}),owner=createShellHomeDocument(hub,service as never);
  owner.document.setOpen(true);owner.document.invalidate();await time.advance(0);
  assert.equal(reads,1);assert.equal(owner.document.getState().stale,true);assert.equal(owner.document.getState().failures,1);
  assert.equal(new HomeReadNotAnswered().retryable,true);owner.dispose();hub.dispose();
});

test('first-load failure owes normal recovery without a hint, minute clock or forced reopen', async () => {
  let reads=0;const error=new Error('first read unavailable');
  const service=new HomeService({request:async<T>()=>{if(++reads===1)throw error;return manifest() as T;}});
  const time=clock(),hub=new FreshnessHub({...time.env,gatherMs:0,settleMs:0,random:()=>0}),owner=createShellHomeDocument(hub,service);owner.document.setOpen(true);
  await service.load();await time.advance(0);assert.equal(reads,2);assert.equal(service.getSnapshot().phase,'ready');
  assert.equal(owner.document.getState().stale,false);owner.dispose();service.dispose();hub.dispose();
});
test('layout preference adoption retains closed Home display debt with zero hidden rows or publication', async () => {
  let reads=0,authority=0,settings=0,published=0;const service=new HomeService({request:async<T>()=>{reads++;return manifest() as T;}});
  await service.load();const first=service.getSnapshot().document;
  const time=clock(),hub=new FreshnessHub({...time.env,gatherMs:0,settleMs:0,random:()=>0}),owner=createShellHomeDocument(hub,service),listeners=new Set<()=>void>();
  let preference={effective:{'home.rowOrder':['films'],'home.hiddenRowIds':[]}};
  const offPublish=service.subscribe(()=>{published++;});
  const off=watchShellHomePreferences({read:()=>preference as never,subscribe:fn=>{listeners.add(fn);return()=>listeners.delete(fn);},document:owner.document,isCurrent:()=>true,
    refreshSettings:()=>{settings++;},authority:()=>{authority++;},changed:()=>{}});
  preference={effective:{'home.rowOrder':['continue','films'],'home.hiddenRowIds':[]}};for(const fn of listeners)fn();await time.advance(0);
  assert.equal(authority,1);assert.equal(settings,1);assert.equal(reads,1);assert.equal(published,0);assert.equal(service.getSnapshot().document,first);
  assert.equal(owner.document.getState().stale,true);off();offPublish();owner.dispose();service.dispose();hub.dispose();
});
