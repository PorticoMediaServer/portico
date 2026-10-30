import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQueueSnapshot,parseQueueMutation} from '../src/queues.ts';
const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const entry=(id:string)=>({id,hidden:false,removed:false,itemId:'song',editionId:null,partId:null,sourceContext:{kind:'item',id:'song',revision:null,entryId:null}});
const snapshot=()=>({id:'queue',scope,revision:'1',highestSeenSequence:'0',replayWindow:{results:256,bodies:32},repeat:'off',shuffled:false,currentEntryId:null,currentPlaybackId:null,entries:[entry('first'),entry('second')]});
test('queue parser retains duplicate media with distinct stable occurrences',()=>{
 const parsed=parseQueueSnapshot(snapshot(),scope,'queue');assert.deepEqual(parsed.entries.map(e=>e.id),['first','second']);assert.ok(Object.isFrozen(parsed.entries));
});
test('queue parser rejects another controller or retired epoch',()=>{
 for(const key of ['serverId','profileId','controllerId','controllerEpoch','commandLaneId']){const value={...snapshot(),scope:{...scope,[key]:'other'}};assert.throws(()=>parseQueueSnapshot(value,scope));}
});
test('queue parser rejects duplicate identities, unsafe counters and invalid current tombstones',()=>{
 const duplicate=snapshot();duplicate.entries[1].id='first';assert.throws(()=>parseQueueSnapshot(duplicate,scope));
 const overflow=snapshot();overflow.revision='9223372036854775808';assert.throws(()=>parseQueueSnapshot(overflow,scope));
 const removed=snapshot();removed.entries[0].removed=true;assert.throws(()=>parseQueueSnapshot(removed,scope));
});
test('hidden entries cannot retain media or source metadata',()=>{
 const value={...snapshot(),entries:[{id:'hidden',hidden:true,removed:false}]};assert.equal(parseQueueSnapshot(value,scope).entries[0].hidden,true);
 assert.throws(()=>parseQueueSnapshot({...value,entries:[{...value.entries[0],itemId:'secret'}]},scope));
});

test('historical acceptance is distinct from current revision and bounded watermark',()=>{
 const value={...snapshot(),revision:'8',highestSeenSequence:'9',editReceipt:{operationId:'op',sequence:'2',disposition:'accepted',acceptedRevision:'3',replayed:true,bodyCompacted:true}};
 const parsed=parseQueueSnapshot(value,scope);assert.equal(parsed.revision,'8');assert.equal(parsed.editReceipt?.acceptedRevision,'3');
 for(const delta of [{sequence:'10'},{acceptedRevision:'9'},{disposition:'conflict'},{replayed:false}])assert.throws(()=>parseQueueSnapshot({...value,editReceipt:{...value.editReceipt,...delta}},scope));
 assert.throws(()=>parseQueueSnapshot({...value,highestSeenSequence:'01'},scope));
 assert.throws(()=>parseQueueSnapshot({...value,replayWindow:{results:257,bodies:32}},scope));
});
test('journal intent validates positive sequence and freezes copied complete action',()=>{
 const ids=['b','a'];const parsed=parseQueueMutation({operationId:'op',sequence:'2',expectedRevision:'1',action:'reorder',entryIds:ids});ids.reverse();assert.deepEqual((parsed as {entryIds:readonly string[]}).entryIds,['b','a']);assert.ok(Object.isFrozen(parsed));
 for(const sequence of ['0','-1','01','9223372036854775808'])assert.throws(()=>parseQueueMutation({operationId:'op',sequence,expectedRevision:'1',action:'repeat',repeat:'one'}));
 assert.throws(()=>parseQueueMutation({operationId:'op',sequence:'1',expectedRevision:'1',action:'reorder',entryIds:['a','a']}));
});

test('bulk queue journal copies complete inputs and preserves intentional duplicate songs',()=>{
 const {id,hidden,removed,...song}=entry('unused');
 const inputs=[song,{...song}];
 const parsed=parseQueueMutation({operationId:'bulk',sequence:'1',expectedRevision:'1',action:'insert-next',entries:inputs});
 inputs[0].itemId='changed';inputs.pop();
 assert.equal(parsed.action,'insert-next');
 if(parsed.action!=='insert-next')throw new Error('wrong action');
 assert.equal(parsed.entries.length,2);assert.equal(parsed.entries[0].itemId,'song');
 assert.ok(Object.isFrozen(parsed.entries));assert.ok(Object.isFrozen(parsed.entries[0].sourceContext));
});
test('bulk queue journal rejects partial, oversized and preassigned occurrence inputs',()=>{
 const {id,hidden,removed,...song}=entry('unused');
 const body={operationId:'bulk',sequence:'1',expectedRevision:'1',action:'append'};
 for(const entries of [[],Array.from({length:1001},()=>song),[song,{...song,id:'injected'}],[song,{itemId:'song'}]])assert.throws(()=>parseQueueMutation({...body,entries}));
 assert.throws(()=>parseQueueMutation({...body,entries:[song],entry:song}));
});
