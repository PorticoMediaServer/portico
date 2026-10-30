import test from 'node:test';import assert from 'node:assert/strict';
import {parseSegmentMarkers,parseSegmentMarkerSet,canSkipAutomatically,parseQueuePostPlay,passoutCheckRequired} from '../src/index.ts';
const marker=(over:Record<string,unknown>={})=>({id:'marker-a',kind:'intro',startMs:5000,endMs:35000,automaticSafe:true,...over});
const postPlay=(over:Record<string,unknown>={})=>({nextEntryId:'entry-b',available:true,reason:'ready',countdownMs:10000,autoplay:true,passoutCheckDue:false,automaticAdvances:0,...over});
test('markers keep the server skip decision and reject invented fields or orders',()=>{
 const markers=parseSegmentMarkers([marker(),marker({id:'marker-b',kind:'credits',startMs:80000,endMs:100000,automaticSafe:false})],120);
 assert.equal(markers.length,2);
 assert.equal(canSkipAutomatically(markers[0]),true);
 assert.equal(canSkipAutomatically(markers[1]),false);
 assert.throws(()=>parseSegmentMarkers([marker({confidence:.9})]));
 assert.throws(()=>parseSegmentMarkers([marker({kind:'chapter'})]));
 assert.throws(()=>parseSegmentMarkers([marker({automaticSafe:'yes'})]));
 assert.throws(()=>parseSegmentMarkers([marker({endMs:5000})]));
 assert.throws(()=>parseSegmentMarkers([marker(),marker({id:'marker-b',startMs:1000,endMs:2000})]));
 assert.throws(()=>parseSegmentMarkers([marker(),marker()]));
 assert.throws(()=>parseSegmentMarkers([marker({endMs:200000})],120));
});
test('a marker set is refused when it belongs to another source',()=>{
 const set=parseSegmentMarkerSet({sourceId:'asset','revision':'rev1',markers:[marker()]},'asset',120);
 assert.equal(set.markers.length,1);
 assert.throws(()=>parseSegmentMarkerSet({sourceId:'other',revision:'rev1',markers:[]},'asset'));
 assert.throws(()=>parseSegmentMarkerSet({sourceId:'asset',markers:[]},'asset'));
});
test('post-play must agree with the queue next entry and publish server preferences',()=>{
 const parsed=parseQueuePostPlay(postPlay(),{entryId:'entry-b',available:true,reason:'ready'});
 assert.equal(parsed.countdownSeconds,10);
 assert.equal(parsed.autoplay,true);
 assert.equal(parsed.automaticAdvances,0);
 assert.throws(()=>parseQueuePostPlay(postPlay(),{entryId:'entry-c',available:true,reason:'ready'}));
 assert.throws(()=>parseQueuePostPlay(postPlay({countdownMs:7000})));
 assert.throws(()=>parseQueuePostPlay(postPlay({available:true,nextEntryId:null})));
 assert.throws(()=>parseQueuePostPlay(postPlay({passoutAfterEpisodes:3})));
 const due=parseQueuePostPlay(postPlay({passoutCheckDue:true,automaticAdvances:3}));
 assert.equal(due.passoutCheckDue,true);
 // Starting the next title during the credits is the viewer's choice, off unless the server says on.
 assert.equal(parsed.startDuringCredits,false);
 assert.equal(parseQueuePostPlay(postPlay({startDuringCredits:true})).startDuringCredits,true);
 assert.throws(()=>parseQueuePostPlay(postPlay({startDuringCredits:'yes'})));
});
test('the passout refusal code',()=>{
 assert.equal(passoutCheckRequired,'passout_check_required');
});
