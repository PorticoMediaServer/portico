import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultSeriesChoice, previewRule} from '../src/screens/live/programme-bindings';
import type {GuideChannel, GuideProgramme} from '@core/channel-guide.ts';

const source = 'a'.repeat(48), channelId = 'b'.repeat(64), programmeId = 'c'.repeat(64);
const channel: GuideChannel = {
  id: channelId, sourceId: source, provenance: 'live-source', name: 'Atlas Documentaries', number: '501', group: 'Documentary',
  generation: 'd'.repeat(48), programmes: [], tuneAvailable: true, recordAvailable: true,
  tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 4,
};
const programme = (patch: Partial<GuideProgramme> = {}): GuideProgramme => ({
  id: programmeId, channelId, title: 'The Clockmakers', start: '2026-10-21T19:15:00Z', end: '2026-10-21T20:00:00Z',
  lineage: 'provider-id', seriesId: 'series-1', episodeId: '', newEvidence: 'unknown', description: '', ...patch,
});
const draft = (p: GuideProgramme) => ({channel, programme: p, series: true});

test('a guide series id produces the same all-episodes rule the Series rules editor saves', () => {
  const config = previewRule(draft(programme()), {...defaultSeriesChoice, episodes: 'all', keep: '5', startEarly: 1, endLate: 2, anyChannel: false});
  assert.equal(config.matchKind, undefined);
  assert.equal(config.seriesId, 'series-1');
  assert.deepEqual(config.allowedChannels, [channelId]);
  assert.deepEqual(config.blockedChannels, []);
  assert.equal(config.options.episodeLimit, 5);
  assert.equal(config.options.beforeSeconds, 60);
  assert.equal(config.options.afterSeconds, 120);
});

test('Any channel clears the channel scope only for a provider series', () => {
  const anyChannel = previewRule(draft(programme()), {...defaultSeriesChoice, anyChannel: true});
  assert.deepEqual(anyChannel.allowedChannels, []);
  const titleMatch = previewRule(draft(programme({seriesId: ''})), {...defaultSeriesChoice, anyChannel: true});
  assert.deepEqual(titleMatch.allowedChannels, [channelId]);
});

test('a guide without series ids previews the real title-and-channel match the server accepts', () => {
  const config = previewRule(draft(programme({seriesId: '', title: 'Story Time Island'})), {...defaultSeriesChoice, episodes: 'all'});
  assert.equal(config.matchKind, 'title');
  assert.equal(config.title, 'Story Time Island');
  assert.equal(config.seriesId, '');
  assert.equal(config.channelId, channelId);
  assert.equal(config.episodes, 'all');
});

test('Keep every episode is the zero limit and the drawn default is new episodes on this channel', () => {
  const config = previewRule(draft(programme()), {...defaultSeriesChoice, keep: 'all'});
  assert.equal(config.options.episodeLimit, 0);
  assert.equal(config.episodes, 'new');
  assert.deepEqual(config.allowedChannels, [channelId]);
  assert.equal(defaultSeriesChoice.startEarly, 0);
  assert.equal(defaultSeriesChoice.endLate, 0);
});
