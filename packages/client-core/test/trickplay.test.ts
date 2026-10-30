import test from 'node:test';import assert from 'node:assert/strict';
import {parseTrickplay,trickplayFrame,trickplayFrameAt,trickplayTileUrl,trickplayThumbnailsUrl,preferredTrickplaySet} from '../src/trickplay.ts';
const setId='a'.repeat(64),revision='b'.repeat(64),otherId='c'.repeat(64),otherRevision='d'.repeat(64);
const target={libraryId:'library',itemId:'item'};
function set(overrides:Record<string,unknown>={}):any{
 const id=String(overrides.id??setId);
 return {id,sourceId:'asset',sourceRevision:'rev',width:640,height:360,tileWidth:320,tileHeight:180,columns:2,rows:2,intervalMs:10000,durationMs:60000,sourceOffsetMs:0,tileCount:2,frameCount:6,stale:false,revision,
  tilesUrl:'/v1/items/item/trickplay/'+id+'/tiles',thumbnailsUrl:'/v1/items/item/trickplay/'+id+'/thumbnails.vtt',...overrides};
}
function view(sets:any[]=[set()]):any{return {scope:{serverId:'server',libraryId:'library',itemId:'item',viewerFence:'fence',sessionId:'',sessionGeneration:0},sets};}

test('a consistent descriptor parses and publishes its server-authored URLs',()=>{
 const parsed=parseTrickplay(view(),'server',target);
 assert.equal(parsed.sets.length,1);
 const s=parsed.sets[0];
 assert.equal(s.id,setId);assert.equal(s.columns,2);assert.equal(s.tileCount,2);assert.equal(s.stale,false);
 assert.equal(trickplayThumbnailsUrl(s),'/v1/items/item/trickplay/'+setId+'/thumbnails.vtt');
 assert.equal(trickplayTileUrl(s,1),'/v1/items/item/trickplay/'+setId+'/tiles/1.jpg');
 assert.throws(()=>trickplayTileUrl(s,2));
 assert.throws(()=>trickplayTileUrl(s,-1));
 assert.equal(Object.isFrozen(parsed.sets),true);
});

test('frames map to the right sheet cell and position',()=>{
 const s=parseTrickplay(view(),'server',target).sets[0];
 const first=trickplayFrame(s,0);
 assert.deepEqual([first.tileIndex,first.x,first.y,first.width,first.height],[0,0,0,320,180]);
 assert.deepEqual([first.startSeconds,first.endSeconds],[0,10]);
 const third=trickplayFrame(s,2);
 assert.deepEqual([third.tileIndex,third.x,third.y],[0,0,180]);
 const fifth=trickplayFrame(s,4);
 assert.deepEqual([fifth.tileIndex,fifth.x,fifth.y],[1,0,0]);
 assert.equal(fifth.tileUrl,'/v1/items/item/trickplay/'+setId+'/tiles/1.jpg');
 // The last frame is clamped to the item duration, never past it.
 const last=trickplayFrame(s,5);
 assert.deepEqual([last.startSeconds,last.endSeconds],[50,60]);
 assert.throws(()=>trickplayFrame(s,6));
 assert.equal(trickplayFrameAt(s,0)!.index,0);
 assert.equal(trickplayFrameAt(s,25)!.index,2);
 assert.equal(trickplayFrameAt(s,59.9)!.index,5);
 assert.equal(trickplayFrameAt(s,60),null);
 assert.equal(trickplayFrameAt(s,-1),null);
 assert.equal(trickplayFrameAt(s,NaN),null);
});

test('inconsistent geometry, counts, identifiers, scope and foreign URLs are rejected',()=>{
 const mutations:Record<string,unknown>[]=[
  {width:641},{height:361},{columns:0},{rows:0},{tileWidth:0},
  {tileCount:1},{tileCount:3},{frameCount:0},
  {intervalMs:0},{intervalMs:-1000},{durationMs:0},{sourceOffsetMs:-1000},{sourceOffsetMs:'0'},
  {id:'short'},{revision:'short'},{sourceId:''},{sourceRevision:''},
  {stale:'yes'},
  {tilesUrl:'/v1/items/other/trickplay/'+setId+'/tiles'},
  {tilesUrl:'https://elsewhere.example/tiles'},
  {thumbnailsUrl:'/v1/items/item/trickplay/'+setId+'/thumbs.vtt'},
 ];
 for(const mutation of mutations){
  assert.throws(()=>parseTrickplay(view([set(mutation)]),'server',target),{code:'invalid_trickplay'},JSON.stringify(mutation));
 }
 assert.throws(()=>parseTrickplay(view(),'other-server',target));
 assert.throws(()=>parseTrickplay(view(),'server',{libraryId:'other',itemId:'item'}));
 assert.throws(()=>parseTrickplay({...view(),scope:{...view().scope,sessionId:'session'}},'server',target));
 assert.throws(()=>parseTrickplay({...view(),scope:{...view().scope,viewerFence:''}},'server',target));
 assert.throws(()=>parseTrickplay({...view(),sets:'nope'},'server',target));
 assert.throws(()=>parseTrickplay(null,'server',target));
 // Duplicate set identifiers cannot be told apart by a delivery URL.
 assert.throws(()=>parseTrickplay(view([set(),set()]),'server',target));
});

test('an empty list parses and set preference favours current, finer sets',()=>{
 const empty=parseTrickplay(view([]),'server',target);
 assert.equal(empty.sets.length,0);
 assert.equal(preferredTrickplaySet(empty),null);
 const stale=set({stale:true,intervalMs:2000});
 const current=set({id:otherId,revision:otherRevision,sourceId:'asset-2'});
 const parsed=parseTrickplay(view([stale,current]),'server',target);
 assert.equal(preferredTrickplaySet(parsed)!.id,otherId);
 assert.equal(preferredTrickplaySet(parsed,'asset')!.id,setId);
 assert.equal(preferredTrickplaySet(parsed,'missing'),null);
});

test('a trimmed association indexes frames by source time but reports item time',()=>{
 // The item starts 20s into the source and runs 40s; frame 2 covers its start.
 const s=parseTrickplay(view([set({sourceOffsetMs:20000,durationMs:40000})]),'server',target).sets[0];
 const first=trickplayFrameAt(s,0)!;
 assert.equal(first.index,2);
 assert.deepEqual([first.startSeconds,first.endSeconds],[0,10]);
 const later=trickplayFrameAt(s,15)!;
 assert.equal(later.index,3);
 assert.deepEqual([later.startSeconds,later.endSeconds],[10,20]);
 const last=trickplayFrameAt(s,39.5)!;
 assert.equal(last.index,5);
 assert.deepEqual([last.startSeconds,last.endSeconds],[30,40]);
 assert.equal(trickplayFrameAt(s,40),null);
});
