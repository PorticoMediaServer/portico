import test from 'node:test';
import assert from 'node:assert/strict';
import {measurementSeries,appendMeasurement} from '../src/measurement-history.ts';
import type {Measurement} from '../src/console.ts';
const reading=(at:number,value:unknown):Measurement=>({name:'network',observedAt:at,freshUntil:at+30000,facts:{counter:{state:'available',value}}});
test('live charts derive measured rates, exclude resets, stale gaps, absent and duplicate readings',()=>{
 const rate=measurementSeries([reading(1000,1000),reading(6000,2001000)],'counter','bytes');assert.equal(rate[0].at,6000);assert.ok(Math.abs(rate[0].value-3.2)<1e-10);
 assert.deepEqual(measurementSeries([reading(1000,1),reading(6000,3)],'counter','cpu'),[{at:6000,value:40}]);
 assert.deepEqual(measurementSeries([reading(1000,100),reading(6000,1),reading(30000,2),reading(35000,null)],'counter','bytes'),[]);
 const previous=[reading(5000,10)];assert.deepEqual(appendMeasurement(previous,reading(4000,9)),previous);
 let all:Measurement[]=[];for(let i=0;i<100;i++)all=appendMeasurement(all,reading(i*5000,i));assert.equal(all.length,61);assert.equal(all[0].observedAt,195000);
});
