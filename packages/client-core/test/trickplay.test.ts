import test from 'node:test';import assert from 'node:assert/strict';
import {parseScrubPreviews,serverOffersScrubPreviews,trickplayFrame,trickplayFrameAt,trickplayTileUrl, downloadTrickplaySet} from '../src/trickplay.ts';
const sheet=(n:number)=>'/v1/items/item/scrub-previews?version=med_1&sheet='+n;
// Sol-1's answer: 320x180 cells, 10 columns by 10 rows, one frame per 10 s, sheets in time order.
const answer=(overrides:Record<string,unknown>={})=>({version:'med_1',available:true,durationMs:1_500_000,intervalMs:10_000,frameWidth:320,frameHeight:180,columns:10,frameCount:150,sheets:[sheet(0),sheet(1)],...overrides});

test('a scrub-preview answer becomes the set the players draw; frame n is on sheet n/100 at its cell',()=>{
 const s=parseScrubPreviews(answer(),'item','med_1')!;
 assert.deepEqual([s.columns,s.rows,s.tileCount,s.frameCount,s.width,s.height],[10,10,2,150,3200,1800]);
 assert.equal(trickplayTileUrl(s,1),sheet(1));assert.throws(()=>trickplayTileUrl(s,2));
 const at=trickplayFrameAt(s,1234)!;
 // floor(1234 s / 10 s) = frame 123: sheet 1, column 3, row 2.
 assert.deepEqual([at.index,at.tileIndex,at.x,at.y,at.tileUrl],[123,1,960,360,sheet(1)]);
 assert.deepEqual([trickplayFrame(s,149).startSeconds,trickplayFrame(s,149).endSeconds],[1490,1500]);
 assert.equal(trickplayFrameAt(s,1500),null,'nothing past the end');
 assert.equal(parseScrubPreviews({version:'med_1',available:false},'item','med_1'),null,'none made: the plain scrubber stays');
});

test('a sheet address the answer did not make for this title and version, or a grid it does not fill, is refused',()=>{
 for(const bad of [{sheets:[sheet(0)]},{sheets:[sheet(0),'/v1/items/other/scrub-previews?version=med_1&sheet=1']},{sheets:[sheet(0),sheet(0)]},
  {sheets:[sheet(0),'https://elsewhere.test'+sheet(1)]},{version:'med_2'},{columns:0},{intervalMs:0}])
  assert.throws(()=>parseScrubPreviews(answer(bad),'item','med_1'),{code:'invalid_trickplay'},JSON.stringify(bad));
});

test('previews are asked for only from a server that names them in its capabilities',()=>{
 assert.equal(serverOffersScrubPreviews({capabilities:['queue-transitions']}),false);
 assert.equal(serverOffersScrubPreviews({capabilities:['queue-transitions','scrub-previews']}),true);
 assert.equal(serverOffersScrubPreviews(undefined),false);
});

test('queue 6: a download\'s kept sheets make its preview set, and a half-kept or wrong grid makes none', () => {
  const bundle = {id: 'c'.repeat(64), tileWidth: 160, tileHeight: 90, columns: 5, rows: 5, intervalSeconds: 4, frameCount: 30, durationSeconds: 120, sourceOffsetSeconds: 0, tiles: [{}, {}]};
  const files = ['file:///d/extras/trickplay-0', 'file:///d/extras/trickplay-1'];
  const set = downloadTrickplaySet(bundle, files)!;
  assert.equal(set.tileCount, 2); assert.equal(set.width, 800); assert.equal(set.stale, false);
  assert.equal(trickplayFrameAt(set, 0)?.tileUrl, files[0]);
  assert.equal(trickplayFrameAt(set, 26 * 4)?.tileUrl, files[1], 'frame 26 is on the second sheet');
  assert.equal(downloadTrickplaySet(bundle, files.slice(0, 1)), null, 'a missing sheet');
  assert.equal(downloadTrickplaySet(bundle, []), null);
  assert.equal(downloadTrickplaySet(undefined, files), null);
  assert.equal(downloadTrickplaySet({...bundle, frameCount: 80}, files), null, 'a grid the sheets don\'t make');
  assert.equal(downloadTrickplaySet({...bundle, intervalSeconds: 0}, files), null);
});

