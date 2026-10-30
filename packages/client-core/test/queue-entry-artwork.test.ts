import test from 'node:test';
import assert from 'node:assert/strict';
import {parseEntry} from '../src/playback-v1/types.ts';

test('a queue entry carries its poster and still for the Up next card, only as this server’s addresses', () => {
  const base = {entryId: 'e1', position: 0, itemId: 'i1', kind: 'episode', title: 'Pilot', available: true};
  const entry = parseEntry({...base, posterUrl: '/v1/items/i1/art/poster', stillUrl: '/v1/metadata/item/i1/art/still?v=ab&w=800'});
  assert.equal(entry.posterUrl, '/v1/items/i1/art/poster');
  assert.equal(entry.stillUrl, '/v1/metadata/item/i1/art/still?v=ab&w=800');
  const foreign = parseEntry({...base, posterUrl: 'https://elsewhere.example/p.jpg', stillUrl: 7});
  assert.equal(foreign.posterUrl, undefined);
  assert.equal(foreign.stillUrl, undefined);
});


test('queue window reads episode and album facts without another item lookup', () => {
 const entry=parseEntry({entryId:'e1',segmentId:'s1',position:2,itemId:'i1',kind:'episode',title:'Pilot',durationMs:120000,showId:'show',seasonId:'season',episode:{seasonNumber:1,number:3},artistId:'artist',artist:'The Band',album:'Album',backdropUrl:'/v1/items/i1/art/backdrop'});
 assert.deepEqual(entry.episode,{seasonNumber:1,number:3}); assert.equal(entry.segmentId,'s1'); assert.equal(entry.artistId,'artist'); assert.equal(entry.album,'Album'); assert.equal(entry.backdropUrl,'/v1/items/i1/art/backdrop');
 assert.equal(parseEntry({...entry,backdropUrl:'https://elsewhere.example/private.jpg'}).backdropUrl,undefined);
});
