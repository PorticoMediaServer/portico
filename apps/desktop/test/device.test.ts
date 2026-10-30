import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createDesktopDevicePort,parseDeviceFacts} from '../src/device.ts';

test('facts-only port exposes exact observed name/version without setter, defaults or updater',()=>{
  const facts=parseDeviceFacts({name:{value:'Studio — 设备',readOnly:true,path:'/private'},appVersion:'0.1.0',update:'ready',document:{revision:'invented'}});
  const port=createDesktopDevicePort(facts),before=port.getSnapshot();
  assert.equal(port.apply,undefined);assert.equal(before.document,undefined);assert.equal(port.restartToUpdate,undefined);
  assert.deepEqual(before,{facts:{name:{value:'Studio — 设备',readOnly:true},appVersion:'0.1.0'}});
  assert.equal(port.getSnapshot(),before);assert.ok(Object.isFrozen(before.facts.name));port.dispose();
});
test('unknown name stays absent and malformed native identity fails closed',()=>{
  assert.deepEqual(parseDeviceFacts({appVersion:'0.1.0'}),{appVersion:'0.1.0'});
  for(const value of [null,[],{appVersion:'latest'},{appVersion:'0.1.0',name:{value:'',readOnly:true}},{appVersion:'0.1.0',name:{value:'x\nprivate',readOnly:true}},{appVersion:'0.1.0',name:{value:'Studio',readOnly:false}}])assert.throws(()=>parseDeviceFacts(value),/device_unavailable/);
});
