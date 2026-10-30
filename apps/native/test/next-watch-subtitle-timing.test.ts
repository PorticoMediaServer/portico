import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence} from '../../../packages/player-core/src/index.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {nativeWatchSubtitleTiming} from '../src-next/screens/watch/subtitle-timing.ts';
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 's', accountId: 'a', profileId: 'p', controllerId: 'c', controllerEpoch: 'e', commandLaneId: 'l', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'v2', entryId: 'entry', generation: 3});
function source(kind = 'ass', delivery = 'device', renderer = 'client'): any {
  const resource = {id: 'sub', sourceId: 'v2', revision: 2, enabled: true, offsetUs: '2000000'};
  return {retired: false, fence, dock: {local: {fence}}, options: parseOptions({itemId: 'item', versions: [{id: 'v2'}]}),
    session: {id: 'session', itemId: 'item', versionId: 'v2', queue: {entryId: 'entry'}, presentation: {generation: 3, subtitle: {trackId: 'sub', kind, delivery}}},
    subtitles: {suppressed: false, plan: {sessionId: 'session', generation: 3, sourceId: 'v2', mode: 'track', selected: resource, resources: [resource], renderer, offsetUs: '7000000'}}};
}
test('timing uses complete current plan offset once and retains actual rich or burn delivery', () => {
  assert.deepEqual(nativeWatchSubtitleTiming(source(), 500), {subtitleTrackId: 'sub', subtitleOffsetMs: 7500, subtitleDelivery: 'embeddedClient'});
  assert.deepEqual(nativeWatchSubtitleTiming(source('ass', 'burn', 'burn_in'), -500), {subtitleTrackId: 'sub', subtitleOffsetMs: 6500, subtitleDelivery: 'burn'});
  assert.deepEqual(nativeWatchSubtitleTiming(source('text', 'sidecar', 'external_text'), 500), {subtitleTrackId: 'sub', subtitleOffsetMs: 7500});
});
test('timing refuses stale/absent selected resources, nonfinite corrections and producer bounds', () => {
  const current = source();
  for (const patch of [{retired: true}, {session: undefined}, {subtitles: {...current.subtitles, suppressed: true}},
    {subtitles: {plan: {...current.subtitles.plan, generation: 4}}}, {subtitles: {plan: {...current.subtitles.plan, mode: 'off', selected: null}}}]) {
    assert.equal(nativeWatchSubtitleTiming({...current, ...patch}, 500), undefined);
  }
  assert.equal(nativeWatchSubtitleTiming(current, Infinity), undefined);
  assert.equal(nativeWatchSubtitleTiming({...current, subtitles: {plan: {...current.subtitles.plan, offsetUs: '600000000'}}}, 500), undefined);
});
