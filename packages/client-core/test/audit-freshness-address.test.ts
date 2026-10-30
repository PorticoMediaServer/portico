import test from 'node:test';
import assert from 'node:assert/strict';
import {parseFreshnessChange} from '../src/freshness.ts';
import {FreshnessHub,dependsOn} from '../src/freshness-hub.ts';
import type {ServerEvent} from '../src/playback-v1/types.ts';

const event=(data:Record<string,unknown>,revision='2'):ServerEvent=>({id:'hint',type:'settings.changed',resource:{kind:'settings',id:'preferences'},revision,data});

test('freshness preserves the preference scope and device address from the hint',()=>{
 const address={scope:'profile-device',deviceClass:'television',deviceId:'device_1'};
 const parsed=parseFreshnessChange(event(address));
 assert.deepEqual(parsed?.preferenceAddress,address);
 assert.ok(Object.isFrozen(parsed?.preferenceAddress));
 for(const data of [{}, {scope:'other'}, {scope:'profile-device'}, {scope:'profile-device-class'}, {scope:'profile-device',deviceId:'../escape'}, {scope:'profile-server',deviceClass:'x'.repeat(1000)}]){
  assert.equal(parseFreshnessChange(event(data))?.preferenceAddress,undefined);
 }
});

test('equal preference revisions invalidate independently by scope, class and device',()=>{
 const hub=new FreshnessHub();let heard=0;
 const stop=hub.watch([dependsOn.settings('preferences')],()=>heard++);
 const addresses=[
  {scope:'profile-server'},
  {scope:'profile-device-class',deviceClass:'web'},
  {scope:'profile-device-class',deviceClass:'mobile'},
  {scope:'profile-device',deviceClass:'television',deviceId:'a'},
  {scope:'profile-device',deviceClass:'television',deviceId:'b'},
 ];
 try{
  for(const address of addresses)hub.accept(event(address));
  assert.equal(heard,addresses.length);
  assert.equal(hub.version(dependsOn.settings('preferences')),addresses.length);
  for(const address of addresses)hub.accept(event(address));
  assert.equal(heard,addresses.length,'same-address replays are deduped');
  hub.accept(event(addresses[0]!,'3'));assert.equal(heard,addresses.length+1);
  hub.resync();assert.equal(heard,addresses.length+2);
  hub.accept(event(addresses[0]!,'3'));assert.equal(heard,addresses.length+3,'resync retires remembered counters');
 }finally{stop();hub.dispose();}
});

test('an absent or malformed preference address cannot suppress a later invalidation',()=>{
 const hub=new FreshnessHub();
 try{
  for(const data of [{}, {}, {scope:'unknown'}, {scope:'profile-device'}, {scope:'profile-server'}])hub.accept(event(data));
  assert.equal(hub.version(dependsOn.settings('preferences')),5);
  hub.accept(event({scope:'profile-server'}));assert.equal(hub.version(dependsOn.settings('preferences')),5);
  const server={...event({}),resource:{kind:'settings',id:'server'}};
  hub.accept(server);hub.accept(server);assert.equal(hub.version(dependsOn.settings('server')),1);
 }finally{hub.dispose();}
});
