import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopWindowController} from '../src/window-controller.ts';
import {createDesktopAppLifetime} from '../src/app-lifetime.ts';
import {installWindowChromeHost,createWindowChromePublisher,getWindowChromeEnvironment} from '../../../web/src/platform/window-chrome.ts';
import type {ChromeUpdate,WindowBridge,WindowSnapshot} from '../src/window.ts';

const observed:WindowSnapshot={os:'windows',fullscreen:false,maximized:false,focused:true,captionHover:0,captions:{minimize:'Minimize',maximize:'Maximize',restore:'Restore',close:'Close'}};
const nextTurn=()=>new Promise<void>(resolve=>setImmediate(resolve));

test('terminal close retires the real app lifetime without waiting on itself',{timeout:1000},async()=>{
  const app=createDesktopAppLifetime(),events:string[]=[];
  await app.importWebApp(async()=>({disposeApp(){events.push('web');}}));
  const controller=createDesktopWindowController({bridge:{async read(){return observed;},async chrome(value){events.push(value.enabled?'chrome-on':'chrome-off');},async action(value){events.push(value);}},onEnvironment(){},onSnapshot(){},onError:()=>assert.fail('unexpected native error'),prepareToClose:()=>app.dispose()});
  app.retain(()=>controller.dispose());
  const closing=controller.action('close');assert.equal(controller.action('close'),closing);
  await closing;await app.dispose();await controller.action('drag');
  assert.deepEqual(events,['web','chrome-off','close']);
});

test('close drains earlier native actions and rejects later writes/read completions',{timeout:1000},async()=>{
  const events:string[]=[];let finish!:()=>void;
  const controller=createDesktopWindowController({bridge:{async read(){events.push('read');return observed;},async chrome(value){events.push(value.enabled?'chrome-on':'chrome-off');},async action(value){events.push(value);if(value==='drag')await new Promise<void>(resolve=>{finish=resolve});}},onEnvironment(){},onSnapshot(){},onError:()=>assert.fail('unexpected native error'),prepareToClose:async()=>{events.push('prepare');await controller.dispose();}});
  const drag=controller.action('drag');await nextTurn();
  const closing=controller.action('close');await controller.action('minimize');await controller.read();
  controller.update({surface:'video',controlsVisible:false,fullscreen:true});controller.geometry(null);
  finish();await drag;await closing;
  assert.deepEqual(events,['drag','prepare','chrome-off','close']);
});

test('failed close preparation never skips app retirement to close the native window',async()=>{
  let failures=0,closed=0;
  const controller=createDesktopWindowController({bridge:{async read(){return observed;},async chrome(){},async action(){closed++;}},onEnvironment(){},onSnapshot(){},onError(){failures++;},prepareToClose:async()=>{throw new Error('controlled retirement failure');}});
  await controller.action('close');assert.equal(failures,1);assert.equal(closed,0);await controller.dispose();
});

test('actual MAIN installation exits DOM fullscreen without sticky native facts and follows native fullscreen changes',async()=>{
  let native=observed,installation:ReturnType<typeof installWindowChromeHost>|undefined;
  const states:boolean[]=[],writes:ChromeUpdate[]=[];
  const controller=createDesktopWindowController({
    bridge:{async read(){return native},async chrome(value){writes.push(value)},async action(){}},
    onEnvironment:value=>installation?.updateEnvironment(value),onSnapshot:(_,state)=>states.push(state.fullscreen),onError:()=>assert.fail('unexpected native error'),
  });
  installation=installWindowChromeHost(controller);
  const publisher=createWindowChromePublisher('video');
  try {
    await controller.read();
    assert.deepEqual(getWindowChromeEnvironment(),{os:'windows',fullscreen:false});
    publisher.update({fullscreen:true,controlsVisible:true});
    assert.equal(states.at(-1),true);
    assert.equal(getWindowChromeEnvironment()?.fullscreen,false);
    publisher.update({fullscreen:false,controlsVisible:true});
    assert.equal(states.at(-1),false);
    native={...native,fullscreen:true};await controller.read();
    assert.equal(getWindowChromeEnvironment()?.fullscreen,true);
    assert.equal(states.at(-1),true);
    native={...native,fullscreen:false};await controller.read();await nextTurn();
    assert.equal(states.at(-1),false);
    assert.equal(writes.at(-1)?.fullscreen,false);
  } finally {publisher.dispose();installation.dispose();await controller.dispose();}
});

test('retirement discards in-flight native facts and cancels queued actions after the executing action',async()=>{
  let completeRead!:(value:WindowSnapshot)=>void,completeAction!:()=>void;
  const actions:string[]=[],writes:ChromeUpdate[]=[],facts:unknown[]=[];
  const bridge:WindowBridge={read:()=>new Promise(resolve=>{completeRead=resolve}),async chrome(value){writes.push(value)},action:action=>{actions.push(action);return new Promise(resolve=>{completeAction=resolve})}};
  const controller=createDesktopWindowController({bridge,onEnvironment:value=>facts.push(value),onSnapshot:()=>assert.fail('late render'),onError:()=>assert.fail('unexpected native error')});
  const reading=controller.read(),first=controller.action('minimize');
  await nextTurn();const queued=controller.action('toggleMaximize'),closing=controller.dispose();
  completeRead(observed);await reading;completeAction();await first;await queued;await closing;
  await controller.action('close');controller.update({surface:'pages',fullscreen:true,controlsVisible:true});controller.geometry(null);await controller.read();
  assert.deepEqual(actions,['minimize']);assert.deepEqual(facts,[]);
  assert.deepEqual(writes,[{enabled:false,surface:'pages',controlsVisible:true,fullscreen:false,captions:null}]);
});

test('native restoration is last even when an older chrome write completes after disposal',async()=>{
  const writes:ChromeUpdate[]=[];let acknowledge!:()=>void;
  const controller=createDesktopWindowController({bridge:{async read(){return observed},chrome:value=>{writes.push(value);return value.enabled?new Promise(resolve=>{acknowledge=resolve}):Promise.resolve()},async action(){}},onEnvironment(){},onSnapshot(){},onError:()=>assert.fail('unexpected native error')});
  await controller.read();controller.update({surface:'music',fullscreen:false,controlsVisible:false});
  const closing=controller.dispose();acknowledge();await closing;
  assert.deepEqual(writes.map(value=>[value.enabled,value.surface]),[[true,'pages'],[false,'pages']]);
});
