import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL(`../src-next/${path}`, import.meta.url), 'utf8');

test('O-159 starting dock: drawn only until the real dock exists, Stop only, focus on Stop on TV', () => {
  const layer = source('shell/NativePlaybackLayer.tsx');
  assert.match(layer, /snapshot\.starting && !snapshot\.dock\.local && !snapshot\.retired && sameAuthority\(snapshot\.authority, authority\) && <StartingDock key=\{snapshot\.starting\.key\}/);
  assert.match(layer, /snapshot\.startTimedOut && !snapshot\.retired && sameAuthority\(snapshot\.authority, authority\) && <StartTimedOut/);
  const dock = source('kit/dock/StartingDock.tsx');
  assert.match(dock, /hasTVPreferredFocus=\{tv\} onPress=\{onStop\}/);
  assert.match(dock, /<ActivityIndicator/);
  for (const forbidden of [/i-pause/, /i-play\b/, /progress/i, /scrub/i]) assert.doesNotMatch(dock.replace(/never shows Pause or\s*\n?\s*\* progress/, ''), forbidden);
  const timedOut = source('shell/StartTimedOut.tsx');
  assert.match(timedOut, /t\('web\.player\.error\.startTitle'\)/);
  assert.match(timedOut, /hasTVPreferredFocus onPress=\{\(\) => \{void timedOut\.retry\(\)/);
});
