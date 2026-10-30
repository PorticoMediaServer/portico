import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createLibraryRouteProof, createLibraryRouteGuard} from '../src/shell/library-route-proof.ts';
import {createShellFactsSource} from '../src/shell/fact-source.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
const turn=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};
for(const order of ['directory-first','home-first'] as const)test('actual parent adapter patch keeps one Home manifest and retires before display settlement: '+order,async()=>{
  const interests=new Map<string,any>(), session={sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}};
  let homeCalls=0,directoryCalls=0,release!:()=>void;const settle=new Promise<void>(resolve=>{release=resolve;});
  const hub={interest(options:any){const state={options,invalidations:0,invalidate(){this.invalidations++;},dispose(){},setRetained(){}};interests.set(options.name,state);return state;},settled:()=>settle};
  let owner:any;const proof=createLibraryRouteProof({session:()=>session,isCurrent:()=>true,directoryVersion:()=>0,homeVersion:()=>0,request:()=>owner.request('directory')});
  const module=await componentModule(new URL('../src/shell/facts.ts',import.meta.url),{'./fact-source':{createShellFactsSource},'../../../packages/contracts/src/routes.ts':{routePath},'@core/freshness-hub.ts':{dependsOn,isSharedReadSuperseded:()=>false}});
  const initial={serverName:'Server',preferences:{},identity:{viewer:session.viewer,libraries:[{id:'target',name:'Before',kind:'book'}]}};
  owner=module.createShellFactOwner({api:{request:async(path:string)=>{assert.equal(path,routePath('get_libraries'));directoryCalls++;return{items:[{id:'other',name:'Other',kind:'book'}]};}},session:()=>session,auth:{},preferences:{},hub,initial,witnesses:{},readonlyAccess:true,isCurrent:()=>true,retire(){throw new Error('unexpected epoch replacement');},libraryRouteProof:proof,
    home:{authority:async()=>{homeCalls++;proof.observeHome({serverId:'server',viewerFence:'accepted'});return'answered';}}});
  const navigations:any[]=[];const guard=createLibraryRouteGuard({source:proof.source,opening:{},isCurrent:()=>true,canReplace:()=>true,libraryId:'target',replace(value){assert.equal(guard.isCurrent(),false);navigations.push(value);},reportError(error){throw error;}});guard.start();
  const directory=interests.get('shell-directory'),home=interests.get('shell-home-authority'),ctx={signal:new AbortController().signal};
  const initialInvalidations=home.invalidations;let pending:Promise<unknown>;
  if(order==='home-first'){await home.options.read(ctx);pending=directory.options.read(ctx);await turn();}
  else {pending=directory.options.read(ctx);await turn();await home.options.read(ctx);}
  assert.equal(homeCalls,1);assert.equal(directoryCalls,1);assert.equal(home.invalidations,initialInvalidations);assert.deepEqual(navigations,[{to:'/libraries',replace:true}]);
  assert.equal(owner.source.getSnapshot().identity.libraries[0].id,'target','input settling does not delay proof retirement');
  release();await pending;guard.dispose();owner.dispose();proof.dispose();
});
