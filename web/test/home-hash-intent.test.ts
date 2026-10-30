import assert from 'node:assert/strict';
import {test} from 'node:test';
import {sectionIntent} from '../src/services/HomePage.tsx';

// FX177: only the supported, non-secret section anchors become Home intent. P2-66 adds
// #local: the app's chooser at web.getportico.tv opens the typed-address page here.
// Every other fragment stays inert so arbitrary hashes can never navigate.

test('the supported Join, Set up and local-address anchors become section intent', () => {
  assert.equal(sectionIntent('#join'), 'join');
  assert.equal(sectionIntent('#setup'), 'setup');
  assert.equal(sectionIntent('#local'), 'local');
});

test('every other fragment is refused', () => {
  for (const hash of ['', '#', '#joi', '#JOIN', '#join/', '#setup?x=1', '#account', '#LOCAL', '#local/', '#join-setup', '#%6Aoin', '#join&setup', '#security']) {
    assert.equal(sectionIntent(hash), undefined, hash);
  }
});
