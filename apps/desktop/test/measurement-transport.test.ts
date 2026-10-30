import test from 'node:test';
import assert from 'node:assert/strict';
import {collectDesktopMeasurement} from '../src/measurement-transport.ts';

const sample=(density=1)=>({measurement:{renderer:'dom',coordinateUnit:'css-px'},viewport:{deviceScaleFactor:density},fontStatus:'loaded',fontFaces:[{status:'loaded'}]});
test('transport writes the unchanged deterministic collector result once',async()=>{
  for(const density of [1,2,3]){
  const value=sample(density),options={},writes:unknown[]=[];let calls=0;
  await collectDesktopMeasurement(options,{measure:async input=>{assert.equal(input,options);calls++;return value;},validate:()=>[],focus:()=>null,write:async result=>{writes.push(result);}},new AbortController().signal);
  assert.equal(calls,2);assert.deepEqual(writes,[value]);assert.equal(writes[0],value);
  assert.equal(value.viewport.deviceScaleFactor,density);
  }
});
test('unstable facts/focus and validator failures never write',async()=>{
  for(const mode of ['facts','focus','validation']){
    let calls=0,focus=0,writes=0;
    await assert.rejects(collectDesktopMeasurement({}, {measure:async()=>({...sample(),value:mode==='facts'?calls++:0}),validate:()=>mode==='validation'?['root.viewport']:[],focus:()=>mode==='focus'?focus++:null,write:async()=>{writes++;}},new AbortController().signal));
    assert.equal(writes,0);
  }
});
test('unloaded fonts and failed fonts remain unmeasured',async()=>{
  for(const value of [{...sample(),fontFaces:[]},{...sample(),fontFaces:[{status:'error'}]},{...sample(),fontStatus:'loading'}]){
    let writes=0;await assert.rejects(collectDesktopMeasurement({}, {measure:async()=>value,validate:()=>[],focus:()=>null,write:async()=>{writes++;}},new AbortController().signal));assert.equal(writes,0);
  }
});
test('retirement during collection prevents a late native write',async()=>{
  const life=new AbortController();let writes=0;
  await assert.rejects(collectDesktopMeasurement({}, {measure:async()=>{life.abort();return sample();},validate:()=>[],focus:()=>null,write:async()=>{writes++;}},life.signal));assert.equal(writes,0);
});
