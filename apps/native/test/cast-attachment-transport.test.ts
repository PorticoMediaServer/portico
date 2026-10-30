import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
const A='11111111-1111-4111-8111-111111111111',B='22222222-2222-4222-8222-222222222222';
const held=<T,>()=>{let resolve!:(value:T)=>void;const promise=new Promise<T>(done=>{resolve=done;});return{promise,resolve};};
async function transport(){
 const listeners=new Map<string,Set<(event:any)=>void>>(),messages:unknown[]=[],ends:boolean[]=[],queued:(()=>void)[]=[];
 let attachmentId=A,read:(()=>Promise<{deviceName:string;attachmentId:string}|null>)|undefined;
 class Emitter{addListener(name:string,callback:(event:any)=>void){const group=listeners.get(name)??new Set();group.add(callback);listeners.set(name,group);return{remove:()=>group.delete(callback)};}}
 const native={available:async()=>true,current:()=>read?read():Promise.resolve({deviceName:'TV',attachmentId}),choose(){},
  send:(json:string,expected:string)=>new Promise<boolean>((resolve,reject)=>{queued.push(()=>{if(expected!==attachmentId){reject(new Error('retired'));return;}messages.push(JSON.parse(json));resolve(true);});}),
  end:(stop:boolean,expected:string)=>{queued.push(()=>{if(expected===attachmentId)ends.push(stop);});},
 };
 // The transport is not a React module; the harness injects React for the module scope it
 // compiles, so it gets an inert default and nothing here renders.
 const {castTransport}=await screenModule(new URL('../src-next/platform/play-on/cast-transport.ts',import.meta.url),{react:{default:{}},'react-native':{NativeEventEmitter:Emitter,NativeModules:{PorticoCast:native},Platform:{isTV:false}}});
 return{castTransport,messages,ends,queued,select:(value:string)=>{attachmentId=value;},holdRead:(value:typeof read)=>{read=value;},emit:(name:string,event:unknown)=>{for(const callback of [...(listeners.get(name)??[])])callback(event);}};
}
test('queued old native messages cannot enter a new attachment or a retired JS subscription',async()=>{
 const h=await transport(),seen:unknown[]=[];h.select(B);const off=h.castTransport.onMessage((message:unknown)=>seen.push(message),B);
 h.emit('PorticoCastMessage',{attachmentId:A,message:JSON.stringify({type:'status',itemId:'old-private'})});await new Promise(setImmediate);assert.deepEqual(seen,[]);
 const read=held<{deviceName:string;attachmentId:string}|null>();h.holdRead(()=>read.promise);
 h.emit('PorticoCastMessage',{attachmentId:B,message:JSON.stringify({type:'status',itemId:'pending'})});off();read.resolve({deviceName:'TV',attachmentId:B});await new Promise(setImmediate);assert.deepEqual(seen,[]);
});
test('queued bridge delivery requires the same actual attachment at delivery',async()=>{
 const h=await transport(),seen:unknown[]=[];const off=h.castTransport.onMessage((message:unknown)=>seen.push(message),A);
 const read=held<{deviceName:string;attachmentId:string}|null>();h.holdRead(()=>read.promise);
 h.emit('PorticoCastMessage',{attachmentId:A,message:JSON.stringify({type:'status',itemId:'old-private'})});h.select(B);read.resolve({deviceName:'Other TV',attachmentId:B});await new Promise(setImmediate);assert.deepEqual(seen,[]);off();
});
test('outbound proof and Stop carry the original attachment through queued native dispatch; legacy defaults refuse',async()=>{
 const h=await transport();
 const sent=h.castTransport.send({type:'pair',code:'one-use'},A);const refused=assert.rejects(sent);
 h.castTransport.end(true,A);h.select(B);for(const dispatch of h.queued.splice(0))dispatch();await refused;
 assert.deepEqual(h.messages,[]);assert.deepEqual(h.ends,[]);
 await assert.rejects(h.castTransport.send({type:'status'}));h.castTransport.end(true);assert.equal(h.queued.length,0);
});
