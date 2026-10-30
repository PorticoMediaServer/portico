import test from 'node:test';
import assert from 'node:assert/strict';
import {parseStreamOptions} from '../src/console.ts';

test('history facets retain readable rows and validate envelope bounds',async()=>{
 const facets={libraries:[{id:'music',label:'Music'}],viewers:[{id:'local:owner:primary',label:'Owner'}]};
 assert.deepEqual(parseStreamOptions(facets),facets);
 assert.deepEqual(parseStreamOptions({libraries:[{id:'',label:'bad'},...facets.libraries],viewers:facets.viewers}),facets);
 for(const bad of [{libraries:[],viewers:null},{libraries:[],viewers:Array(501).fill({id:'a',label:'b'})}])assert.throws(()=>parseStreamOptions(bad));
});
