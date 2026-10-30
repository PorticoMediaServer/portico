import {test} from 'node:test';
import assert from 'node:assert/strict';
import {elapsedTime,durationLabel} from '../src/media-format.ts';
test('duration and clock formats distinguish short clips, long books and unknown lengths',()=>{
 assert.equal(durationLabel(12),'12 sec');assert.equal(durationLabel(3599),'59 min');assert.equal(durationLabel(3660),'1 hr 1 min');assert.equal(durationLabel(90000),'25 hr');assert.equal(durationLabel(0),'');assert.equal(durationLabel(NaN),'');assert.equal(elapsedTime(3661),'1:01:01');assert.equal(elapsedTime(12),'0:12');assert.equal(elapsedTime(Infinity),'0:00');
});
