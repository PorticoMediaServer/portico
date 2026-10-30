import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopDiscovery,desktopDiscoveryRows} from '../src/discovery.ts';
import {discoveredRoutes} from '../../../packages/client-core/src/route-connection.ts';
const row=()=>({baseUrl:'https://192.168.55.3:10443',serverId:'srv_'+'A'.repeat(43),fingerprint:'B'.repeat(43),port:10443,path:'/',expiresAt:2000});
test('native candidate remains probe-required under genuine core paired-identity admission',()=>{
  const value=desktopDiscoveryRows([row()],1000);assert.equal(value.length,1);assert(Object.isFrozen(value));
  const pin={serverId:row().serverId,publicKey:'C'.repeat(43),fingerprint:row().fingerprint};
  assert.equal(discoveredRoutes(value,pin,1000)[0]?.quality,'probe_required');
  assert.deepEqual(discoveredRoutes(value,{...pin,fingerprint:'D'.repeat(43)},1000),[]);
  assert.deepEqual(discoveredRoutes(value,{...pin,serverId:'srv_'+'E'.repeat(43)},1000),[]);
});
test('expired, public, loopback, URL credentials, paths, surplus and unreported facts fail closed',()=>{
  for(const change of [{expiresAt:1000},{expiresAt:2001},{baseUrl:'https://8.8.8.8:10443'},
    {baseUrl:'https://127.0.0.1:10443'},{baseUrl:'https://user:password@192.168.55.3:10443'},
    {baseUrl:'https://192.168.55.3:10443/v1/'},{baseUrl:'http://192.168.55.3:10443'},
    {baseUrl:'https://192.168.55.3:10443?token=private'},{path:'/arbitrary'},{port:10444},{trusted:true},
    {fingerprint:'private'}, {serverId:'other'}])assert.throws(()=>desktopDiscoveryRows([{...row(),...change}],1000),/^Error: discovery_unavailable$/);
  for(const value of [undefined,{},Array(65).fill(row())])assert.throws(()=>desktopDiscoveryRows(value,1000),/^Error: discovery_unavailable$/);
});
test('aborted native browse settles immediately and cancels only its ID; late answer/failure is ignored',async()=>{
  let complete!:(value:unknown)=>void;const cancellations:number[]=[];
  const owner=createDesktopDiscovery({browse:()=>new Promise(resolve=>{complete=resolve}),async cancel(id){cancellations.push(id)}},()=>1000,()=>19);
  const caller=new AbortController(),pending=owner.discover(caller.signal);caller.abort();
  await assert.rejects(pending,{name:'AbortError'});assert.deepEqual(cancellations,[19]);complete([row()]);
  owner.dispose();await assert.rejects(owner.discover(new AbortController().signal),{name:'AbortError'});
});
test('provider is inert and disposal cancels independent captured calls without exposing native errors',async()=>{
  let count=0;const cancelled:number[]=[];
  const owner=createDesktopDiscovery({browse:async()=>{throw new Error('/private/native details')},async cancel(id){cancelled.push(id)}},()=>1000,()=>++count);
  assert.equal(count,0);await assert.rejects(owner.discover(new AbortController().signal),/^Error: discovery_unavailable$/);assert.equal(count,1);assert.deepEqual(cancelled,[]);owner.dispose();
});
test('even a synchronous cancel bridge failure cannot prevent retirement or surface native details',async()=>{
  let complete!:(value:unknown)=>void;
  const owner=createDesktopDiscovery({browse:()=>new Promise(resolve=>{complete=resolve}),cancel(){throw new Error('/private/native detail')}},()=>1000,()=>31);
  const pending=owner.discover(new AbortController().signal);assert.doesNotThrow(()=>owner.dispose());
  await assert.rejects(pending,{name:'AbortError'});complete([row()]);
});
