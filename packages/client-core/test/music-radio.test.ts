import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueue} from '../src/playback-v1/types.ts';

test('a server radio segment preserves its kind and buffered counts without invented output facts',()=>{
 const queue=parseQueue({id:'q_radio',revision:'1',total:32,remainingCount:31,repeat:'off',current:{entryId:'s_radio-0',position:0},segments:[{id:'g_radio',kind:'radio',label:'Dreams',count:32,state:'ready'}]});
 assert.equal(queue.segments[0].kind,'radio');assert.equal(queue.segments[0].label,'Dreams');assert.equal(queue.total,32);
 assert.equal(queue.remainingCount,31);assert.equal('bySound' in queue,false);
 const future=parseQueue({id:'q_radio',revision:'2',total:64,segments:[{id:'g_radio',kind:'future',label:'Dreams',count:64,state:'ready'}]});
 assert.equal(future.segments[0].kind,'other');assert.equal(future.total,64);
});
