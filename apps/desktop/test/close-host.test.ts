import test from 'node:test';
import assert from 'node:assert/strict';
import {installDesktopCloseHost} from '../src/close-host.ts';
import {createDesktopAppLifetime} from '../src/app-lifetime.ts';
const turn=()=>new Promise<void>(resolve=>setImmediate(resolve));

test('OS close awaits real web/native owners then acknowledges once without a self-wait',{timeout:1000},async()=>{
  const events=new EventTarget(),app=createDesktopAppLifetime(),order:string[]=[];let release!:()=>void;
  const host=installDesktopCloseHost({events,disposeApp:()=>app.dispose(),acknowledge:async()=>{order.push('ack');},onError:()=>assert.fail('unexpected shutdown failure')});
  app.retain(()=>host.dispose());
  app.retain(async()=>{order.push('native-retiring');await new Promise<void>(resolve=>{release=resolve});order.push('native-retired');});
  await app.importWebApp(async()=>({disposeApp(){order.push('web');}}));
  events.dispatchEvent(new Event('portico-native-close'));events.dispatchEvent(new Event('portico-native-close'));await turn();
  assert.deepEqual(order,['web','native-retiring']);release();await turn();
  assert.deepEqual(order,['web','native-retiring','native-retired','ack']);
  events.dispatchEvent(new Event('portico-native-close'));await turn();assert.equal(order.filter(value=>value==='ack').length,1);
});
test('page retirement fences app owners without manufacturing an OS-close acknowledgement',async()=>{
  const events=new EventTarget(),app=createDesktopAppLifetime();let web=0,acks=0;
  const host=installDesktopCloseHost({events,disposeApp:()=>app.dispose(),acknowledge:async()=>{acks++;},onError:()=>assert.fail('unexpected shutdown failure')});
  app.retain(()=>host.dispose());await app.importWebApp(async()=>({disposeApp(){web++;}}));
  events.dispatchEvent(new Event('pagehide'));events.dispatchEvent(new Event('pagehide'));await turn();
  assert.equal(web,1);assert.equal(acks,0);
});
test('failed disposal never acknowledges successful web retirement',async()=>{
  const events=new EventTarget();let errors=0,acks=0;
  const host=installDesktopCloseHost({events,disposeApp:async()=>{throw new Error('controlled failure');},acknowledge:async()=>{acks++;},onError(){errors++;}});
  events.dispatchEvent(new Event('portico-native-close'));await turn();assert.equal(errors,1);assert.equal(acks,0);host.dispose();
});
test('retired close-host listeners cannot close a replacement owner',async()=>{
  const events=new EventTarget();let calls=0;
  const host=installDesktopCloseHost({events,disposeApp:async()=>{calls++;},acknowledge:async()=>{calls++;},onError:()=>assert.fail('unexpected shutdown failure')});
  host.dispose();host.dispose();events.dispatchEvent(new Event('pagehide'));events.dispatchEvent(new Event('portico-native-close'));await turn();assert.equal(calls,0);
});
