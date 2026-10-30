import assert from 'node:assert/strict';
import test from 'node:test';
import {parseAttention,parseAttentionItem} from '../src/attention.ts';
import {resetSkippedParts,skippedParts} from '../src/tolerant.ts';

const item=(over:Record<string,unknown>={})=>({id:'ffmpeg',severity:'critical',title:'ffmpeg is missing',detail:'Conversions depend on it.',action:{kind:'dependency',target:'ffmpeg'},...over});

test('a severity this app does not know reads as the plainest one and the item is kept',()=>{
 resetSkippedParts();
 const items=parseAttention({items:[item({id:'a',severity:'urgent'}),item({id:'b',severity:'warning'})]});
 assert.deepEqual(items.map(entry=>[entry.id,entry.severity]),[['a','info'],['b','warning']]);
 assert.equal(items[0].title,'ffmpeg is missing');
 assert.deepEqual(skippedParts(),[{document:'attention',part:'item.severity',count:1}]);
 // A severity that is not a short word at all is a malformed item.
 assert.throws(()=>parseAttentionItem(item({severity:3})),/Invalid/);
 assert.throws(()=>parseAttentionItem(item({severity:'x'.repeat(41)})),/Invalid/);
 assert.throws(()=>parseAttentionItem(item({severity:undefined})),/Invalid/);
});

test('a field this app does not know is ignored',()=>{
 const items=parseAttention({items:[item({futureField:{nested:true},action:{kind:'dependency',target:'ffmpeg',futureField:1}})],futureField:1});
 assert.equal(items.length,1);
 assert.equal(items[0].action.target,'ffmpeg');
});

test('one item that cannot be read is left out and the others keep the server\'s order',()=>{
 resetSkippedParts();
 const items=parseAttention({items:[
  item({id:'first'}),
  item({id:'no-title',title:''}),
  item({id:'no-action',action:undefined}),
  item({id:'long',detail:'x'.repeat(513)}),
  item({id:''}),
  null,
  item({id:'last',severity:'info'}),
 ]});
 assert.deepEqual(items.map(entry=>entry.id),['first','last']);
 assert.deepEqual(skippedParts(),[{document:'attention',part:'items',count:5}]);
 // An item read on its own has nothing else to keep.
 assert.throws(()=>parseAttentionItem(item({title:''})),/Invalid/);
});

test('the list is still bounded and still has to be a list',()=>{
 assert.throws(()=>parseAttention({items:Array.from({length:51},()=>item())}),/Invalid/);
 assert.throws(()=>parseAttention({items:{}}),/Invalid/);
 assert.throws(()=>parseAttention({}),/Invalid/);
 assert.throws(()=>parseAttention(null),/Invalid/);
 assert.throws(()=>parseAttention([item()]),/Invalid/);
 assert.equal(parseAttention({items:Array.from({length:50},(_,n)=>item({id:'i'+n}))}).length,50);
});
