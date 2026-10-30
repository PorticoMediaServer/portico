import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowserServerDisplay} from '../src/platform/server-display.ts';
const pin={serverId:'server_a',publicKey:'key_a',fingerprint:'fingerprint_a'};
const source={pin,origin:'https://address.example',signIn:'local' as const};
const system={id:pin.serverId,name:'Portico demo',signIn:'local',move:null,version:'test',setupRequired:false,hostedConfigured:false,hostedAttached:false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
function fixture(raw:unknown=system){
 const memory=new Map<string,string>(),calls:{origin:string;pin:typeof pin}[]=[];let disposed=0;
 const route={pin,getSnapshot:()=>({phase:'ready'}),dispose:()=>{disposed++;}};
 const api={getRouteConnection:()=>route,request:async(path:string,method:string,body:unknown)=>{assert.equal(path,'/v1/system');assert.equal(method,'GET');assert.equal(body,undefined);return raw;}};
 const storage={getItem:(key:string)=>memory.get(key)??null,setItem:(key:string,value:string)=>{memory.set(key,value);}};
 const environment={storage:{read:async()=>{throw new Error('Names must not read credential storage.');},change:async()=>{throw new Error('Names must not write credential storage.');}}};
 const connect=async(origin:string,checked:typeof pin)=>{calls.push({origin,pin:checked});return {api,connection:route};};
 const display=new BrowserServerDisplay(storage,()=>environment,connect as never);
 return {display,api,memory,calls,disposed:()=>disposed,storage,environment};
}
test('server menu labels come from the pinned server name and cache no credential or address',async()=>{
 const f=fixture();assert.deepEqual(await f.display.choices([source],new AbortController().signal),[{id:pin.serverId,label:'Portico demo'}]);
 assert.deepEqual(f.calls,[{origin:source.origin,pin}]);assert.equal(f.disposed(),1);
 assert.deepEqual([...f.memory.values()].map(value=>JSON.parse(value)),[{version:1,name:'Portico demo'}]);
});
test('a previously verified name survives offline without being replaced by the origin',async()=>{
 const f=fixture();f.display.remember(f.api as never,system.name);
 const offline=new BrowserServerDisplay(f.storage,()=>f.environment,(async()=>{throw new Error('offline');}) as never);
 assert.deepEqual(await offline.choices([source],new AbortController().signal),[{id:pin.serverId,label:'Portico demo'}]);
});
test('another server or a kind mismatch cannot supply display metadata',async()=>{
 for(const raw of [{...system,id:'other_server'},{...system,signIn:'portico'}]){
  const f=fixture(raw);assert.deepEqual(await f.display.choices([source],new AbortController().signal),[]);assert.equal(f.memory.size,0);assert.equal(f.disposed(),1);
 }
});
test('an offline server with no verified name gets no invented address label',async()=>{
 const f=fixture(),offline=new BrowserServerDisplay(f.storage,()=>f.environment,(async()=>{throw new Error('offline');}) as never);
 assert.deepEqual(await offline.choices([source],new AbortController().signal),[]);assert.equal(f.memory.size,0);
});
test('unverified or cancelled display work cannot publish metadata',async()=>{
 const f=fixture();f.display.remember({getRouteConnection:()=>({pin,getSnapshot:()=>({phase:'probing'})})} as never,'Portico demo');assert.equal(f.memory.size,0);
 const abort=new AbortController();abort.abort();await assert.rejects(f.display.choices([source],abort.signal));assert.equal(f.calls.length,0);assert.equal(f.memory.size,0);
});
