import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueuePostPlay} from '../src/post-play.ts';
import {parseQueuePostPlay as playerPostPlay} from '../../player-core/src/post-play.ts';
import {activeSegment,skipTarget,skipsAutomatically} from '../src/segment-skip.ts';
import * as playerSkip from '../../player-core/src/segment-skip.ts';

const raw=(nextEntryId:unknown='v1.entry_2')=>({nextEntryId,available:true,reason:'ready',countdownMs:5000,autoplay:true,passoutCheckDue:false,automaticAdvances:1,startDuringCredits:false});

test('client and player post-play share occurrence parsing without loosening identity bounds',()=>{
 const next={entryId:'v1.entry_2',available:true,reason:'ready'};
 assert.equal(parseQueuePostPlay(raw(),next).nextEntryId,next.entryId);
 assert.equal(parseQueuePostPlay,playerPostPlay);
 assert.throws(()=>parseQueuePostPlay(raw(),{...next,entryId:'v1.entry_3'}));
 for(const id of ['', 'a/b', 'a%2Fb', 'a b', 'a\n', 'a'.repeat(129), 1])assert.throws(()=>parseQueuePostPlay(raw(id)));
 assert.equal(parseQueuePostPlay(raw('a'.repeat(128))).nextEntryId!.length,128);
});

test('client skip operations execute the same canonical player policy',()=>{
 assert.equal(activeSegment,playerSkip.activeSegment);assert.equal(skipTarget,playerSkip.skipTarget);assert.equal(skipsAutomatically,playerSkip.skipsAutomatically);
 const marker={id:'intro',kind:'intro' as const,startSeconds:10,endSeconds:30,automaticSafe:true};
 assert.equal(activeSegment([marker],15,{intro:'ask'},new Set()),marker);
 assert.equal(skipTarget(marker),29);assert.equal(skipsAutomatically(marker,{intro:'auto'}),true);
 assert.equal(skipsAutomatically({...marker,automaticSafe:false},{intro:'auto'}),false);
});
