import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibraryChannelConfig, parseLibraryChannel} from '../src/library-channels.ts';

function minimalQuery(): Record<string, unknown> {
  return {libraryIds: ['lib'], kinds: ['movie'], recentDays: 0, limit: 10, order: 'title'};
}
function minimalRule(): Record<string, unknown> {
  // Another client omits weights and showIds entirely (nil/omitted optional lists).
  return {id: 'r1', name: 'Rule', query: minimalQuery(), mode: 'sequential', episodeMode: 'none', exhaustion: 'loop', deduplicationWindow: 0, maxConsecutive: 1};
}
function minimalConfig(): Record<string, unknown> {
  return {
    protocolVersion: '1.0', id: 'c1', name: 'Channel', description: '', enabled: true,
    position: 0, timezone: 'UTC', seed: 'seed', defaultRuleId: 'r1', viewerAccess: 'owner-only',
    logoItemId: '', templateId: '',
    quality: {mode: 'automatic', maxBitrate: 8000000, maxHeight: 1080, allowLossy: true, allowHdrToSdr: true},
    overlay: {enabled: false, corner: 'br', sizePercent: 10, insetPercent: 5, treatment: 'none'},
    rules: [minimalRule()], blocks: [],
  };
}

test('CD-50: a configuration saved by another client without blocks/weights/showIds decodes', () => {
  const parsed = parseLibraryChannelConfig(minimalConfig());
  assert.deepEqual(parsed.blocks, []);
  assert.deepEqual(parsed.rules[0]!.weights, [], 'nil weights become an empty array');
  assert.deepEqual(parsed.rules[0]!.query.showIds, [], 'nil showIds become an empty array');
  const ch = parseLibraryChannel({
    config: minimalConfig(), revision: 1, state: 'ok', healthCode: '', generation: 'g1',
    generatedThrough: '', candidateCount: 0, unresolvedDurationCount: 0, replacementBoundary: '2026-09-23T12:00:00Z',
  });
  assert.equal(ch.revision, 1);
  assert.equal(ch.replacementBoundary, '2026-09-23T12:00:00Z');
});

test('CD-50: strict scalars are still refused while optional lists are tolerated', () => {
  assert.throws(() => parseLibraryChannelConfig({...minimalConfig(), templateId: 'x'.repeat(129)}), 'dormant templateId stays bounded');
  assert.throws(() => parseLibraryChannelConfig({
    ...minimalConfig(),
    overlay: {enabled: false, corner: 'x'.repeat(41), sizePercent: 10, insetPercent: 5, treatment: 'none'},
  }), 'dormant overlay corner stays bounded');
  assert.throws(() => parseLibraryChannelConfig({
    ...minimalConfig(),
    overlay: {enabled: false, corner: 'br', sizePercent: 101, insetPercent: 5, treatment: 'none'},
  }), 'dormant overlay size stays bounded');
  // Null required collections are never valid on the wire; the server emits arrays.
  assert.throws(() => parseLibraryChannelConfig({...minimalConfig(), blocks: null}));
});
