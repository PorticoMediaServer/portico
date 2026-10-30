import {test} from 'node:test';
import assert from 'node:assert/strict';
import {lintNextGlossary} from '../scripts/lint-next-glossary.mjs';

test('approved state copy is verbatim while unrelated or modified glossary violations still fail', () => {
  assert.deepEqual(lintNextGlossary({
    'next.overlays.reloadHelp': 'Reload to use the new version.',
    'next.overlays.reload': 'Reload',
    'next.states.reload': 'Reload',
    'next.states.playbackInfo': 'Playback info',
  }), []);
  for (const messages of [
    {'next.unrelated': 'Reload'},
    {'next.states.reload': 'Reload everything'},
    {'next.states.playbackInfo': 'Playback info about this'},
    {'next.states.reload': 'Portico Cloud'},
  ]) assert.ok(lintNextGlossary(messages).length > 0);
});

test('approved Home conflict copy is exact and does not exempt other Reload wording', () => {
  const messages = {
    'next.home.layoutChangedBody': 'Your Home layout changed on another device after you opened this. Nothing was overwritten. Reload it to see the latest, then make your changes again.',
    'next.home.layoutChangedBodyCompact': 'Your Home layout changed on another device after you opened this. Nothing was overwritten. Reload it, then make your changes again.',
    'next.home.reloadLayout': 'Reload layout',
  };
  assert.deepEqual(lintNextGlossary(messages), []);
  for (const [key, value] of Object.entries(messages)) {
    assert.ok(lintNextGlossary({[key]: value + ' now'}).length > 0);
    assert.ok(lintNextGlossary({'next.home.unrelated': value}).length > 0);
  }
});
