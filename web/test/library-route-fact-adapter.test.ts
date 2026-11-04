import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createLibraryRouteProof, createLibraryRouteGuard} from '../src/shell/library-route-proof.ts';
import {createShellFactsSource} from '../src/shell/fact-source.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
const turn=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
// Client data step 5: the directory's one answer carries its own viewer fence, so it proves a route alone.
test('actual parent adapter: one directory read proves absence and retires before display settlement, with no Home read',async()=>{
  const interests=new Map<string,any>(), session={sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}};
  let directoryCalls=0,release!:()=>void;const settle=new Promise<void>(resolve=>{release=resolve;});
  const hub={interest(options:any){const state={options,invalidations:0,invalidate(){this.invalidations++;},dispose(){},setRetained(){}};interests.set(options.name,state);return state;},settled:()=>settle};
  let owner:any;const proof=createLibraryRouteProof({session:()=>session,isCurrent:()=>true,directoryVersion:()=>0,request:()=>owner.request('directory')});
  const module=await componentModule(new URL('../src/shell/facts.ts',import.meta.url),{'./fact-source':{createShellFactsSource},'../../../packages/contracts/src/routes.ts':{routePath},'@core/freshness-hub.ts':{dependsOn,isSharedReadSuperseded:()=>false}});
  const initial={serverName:'Server',preferences:{},identity:{viewer:session.viewer,libraries:[{id:'target',name:'Before',kind:'book'}]}};
  owner=module.createShellFactOwner({api:{request:async(path:string)=>{assert.equal(path,routePath('get_libraries'));directoryCalls++;return{items:[{id:'other',name:'Other',kind:'book'}],viewerFence:'accepted',privacyFence:'privacy'};}},session:()=>session,auth:{},preferences:{},hub,initial,witnesses:{},isCurrent:()=>true,retire(){throw new Error('unexpected epoch replacement');},libraryRouteProof:proof});
  const navigations:any[]=[];const guard=createLibraryRouteGuard({source:proof.source,opening:{},isCurrent:()=>true,canReplace:()=>true,libraryId:'target',replace(value){assert.equal(guard.isCurrent(),false);navigations.push(value);},reportError(error){throw error;}});guard.start();
  const directory=interests.get('shell-directory'),ctx={signal:new AbortController().signal};
  assert.equal(interests.has('shell-home-authority'),false,'Home is not read to prove who the viewer is');
  const pending=directory.options.read(ctx);await turn();
  assert.equal(directoryCalls,1);assert.deepEqual(navigations,[{to:'/libraries',replace:true}]);
  assert.equal(owner.source.getSnapshot().identity.libraries[0].id,'target','input settling does not delay proof retirement');
  release();await pending;guard.dispose();owner.dispose();proof.dispose();
});
