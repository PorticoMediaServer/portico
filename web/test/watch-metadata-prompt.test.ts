import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {WatchMetadataPrompt} from '../src/screens/watch/MetadataPrompt.tsx';

// PB-5: the player says when its options couldn't load, with Try again, and only then.
const fence = {kind: 'queue', queueId: 'q1', entryId: 'e1', itemId: 'movie', sessionId: 's1', generation: 1} as never;
const draw = (problem: string | undefined, expanded = true, retried: unknown[] = []) => renderToStaticMarkup(createElement(WatchMetadataPrompt, {
  runtime: {retryMetadata: (shown: unknown) => retried.push(shown)} as never, snapshot: {fence, problem, disposed: false} as never,
  expanded, controlsVisible: true, layout: 'desktop'}));

test('a metadata failure draws the player notice with Try again; nothing else does', () => {
  const html = draw('playback_metadata_failed');
  assert.match(html, /role="status"/);
  assert.match(html, /Playback options couldn’t load\./);
  assert.match(html, /<button type="button">Try again<\/button>/);
  for (const other of [undefined, 'playback_subtitles_failed']) assert.equal(draw(other), '', String(other));
  assert.equal(draw('playback_metadata_failed', false), '', 'the collapsed dock draws no player notice');
});
