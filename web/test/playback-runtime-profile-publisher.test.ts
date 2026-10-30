import test from 'node:test';
import assert from 'node:assert/strict';
import {keepBrowserProfilePublished} from '../src/platform/playback/client-profile.ts';
import {installPlaybackEnvironment} from '../src/platform/playback/environment.ts';
import {webDeclaredProfile} from '../../packages/client-core/src/client-profile.ts';

test('background publication and output changes use the installed native capability provider',async t=>{
  const originalWindow=Object.getOwnPropertyDescriptor(globalThis,'window');
  const originalDevices=Object.getOwnPropertyDescriptor(navigator,'mediaDevices');
  const page=new EventTarget(),devices=new EventTarget();
  Object.defineProperty(globalThis,'window',{configurable:true,value:page});
  Object.defineProperty(navigator,'mediaDevices',{configurable:true,value:devices});
  let stop=()=>{},undo=()=>{};
  t.after(()=>{stop();undo();if(originalWindow)Object.defineProperty(globalThis,'window',originalWindow);else delete (globalThis as any).window;if(originalDevices)Object.defineProperty(navigator,'mediaDevices',originalDevices);else delete (navigator as any).mediaDevices;});
  let calls=0;
  undo=installPlaybackEnvironment({capabilities:async()=>webDeclaredProfile({app:'native-'+(++calls)})});
  const sent:unknown[]=[];
  stop=keepBrowserProfilePublished({requestRaw:async(_path,_method,options)=>{sent.push(options?.body);return {status:204,headers:{}};}},'scope');
  const settle=async()=>{for(let i=0;i<5;i++)await new Promise(setImmediate);};
  await settle();assert.equal((sent[0] as any).client.app,'native-1');
  devices.dispatchEvent(new Event('devicechange'));await settle();
  assert.equal((sent[1] as any).client.app,'native-2','browser probing never overwrites actual installed native facts');
  stop();devices.dispatchEvent(new Event('devicechange'));await settle();assert.equal(sent.length,2);
});

test('failed or unreported installed engine facts never trigger a browser probe or capability overwrite',async t=>{
  const saved=Object.fromEntries(['window','document'].map(key=>[key,Object.getOwnPropertyDescriptor(globalThis,key)]));
  const originalDevices=Object.getOwnPropertyDescriptor(navigator,'mediaDevices');
  const page=new EventTarget(),devices=new EventTarget();let browserProbes=0,mode:'missing'|'reject'|'reported'='missing';
  Object.defineProperty(globalThis,'window',{configurable:true,value:page});
  Object.defineProperty(globalThis,'document',{configurable:true,value:{createElement(){browserProbes++;throw new Error('browser_probe_must_not_run')}}});
  Object.defineProperty(navigator,'mediaDevices',{configurable:true,value:devices});
  const undo=installPlaybackEnvironment({capabilities:(()=>{if(mode==='missing')return undefined;if(mode==='reject')return Promise.reject(new Error('native_output_unavailable'));return Promise.resolve(webDeclaredProfile({app:'confirmed-native'}));}) as NonNullable<import('../src/platform/playback/types.ts').WebPlaybackEnvironment['capabilities']>});
  const sent:unknown[]=[];const stop=keepBrowserProfilePublished({requestRaw:async(_path,_method,options)=>{sent.push(options?.body);return {status:204,headers:{}};}},'scope');
  t.after(()=>{stop();undo();for(const key of ['window','document'] as const){const descriptor=saved[key];if(descriptor)Object.defineProperty(globalThis,key,descriptor);else delete (globalThis as any)[key];}if(originalDevices)Object.defineProperty(navigator,'mediaDevices',originalDevices);else delete (navigator as any).mediaDevices;});
  const settle=async()=>{for(let i=0;i<5;i++)await new Promise(setImmediate)};
  await settle();assert.equal(sent.length,0);assert.equal(browserProbes,0,'an unreported native answer is not permission to probe the WebView');
  mode='reject';devices.dispatchEvent(new Event('devicechange'));await settle();assert.equal(sent.length,0);assert.equal(browserProbes,0);
  mode='reported';devices.dispatchEvent(new Event('devicechange'));await settle();assert.equal((sent[0] as any).client.app,'confirmed-native');assert.equal(browserProbes,0);
});

test('a native capability read resolved after publisher disposal cannot stamp a retired device',async t=>{
  const originalWindow=Object.getOwnPropertyDescriptor(globalThis,'window'),originalDevices=Object.getOwnPropertyDescriptor(navigator,'mediaDevices');
  Object.defineProperty(globalThis,'window',{configurable:true,value:new EventTarget()});Object.defineProperty(navigator,'mediaDevices',{configurable:true,value:new EventTarget()});
  let answer!:(value:ReturnType<typeof webDeclaredProfile>)=>void;const result=new Promise<ReturnType<typeof webDeclaredProfile>>(resolve=>{answer=resolve});
  const undo=installPlaybackEnvironment({capabilities:()=>result});const sent:unknown[]=[];
  const stop=keepBrowserProfilePublished({requestRaw:async(_path,_method,options)=>{sent.push(options?.body);return {status:204,headers:{}};}},'retired-scope');
  t.after(()=>{stop();undo();if(originalWindow)Object.defineProperty(globalThis,'window',originalWindow);else delete (globalThis as any).window;if(originalDevices)Object.defineProperty(navigator,'mediaDevices',originalDevices);else delete (navigator as any).mediaDevices;});
  await new Promise(setImmediate);stop();answer(webDeclaredProfile({app:'too-late-native'}));for(let i=0;i<5;i++)await new Promise(setImmediate);
  assert.equal(sent.length,0);
});
