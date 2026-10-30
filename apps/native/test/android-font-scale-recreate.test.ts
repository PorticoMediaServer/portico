import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const manifest = readFileSync(new URL('../android/app/src/main/AndroidManifest.xml', import.meta.url), 'utf8');

test('a system text-size change recreates the main activity, so text is measured again', () => {
  const activity = manifest.match(/<activity\s+android:name="\.MainActivity"[^>]*>/s)?.[0];
  assert.ok(activity, 'MainActivity is declared');
  const changes = activity.match(/android:configChanges="([^"]*)"/)?.[1]?.split('|') ?? [];
  assert.equal(changes.includes('fontScale'), false);
  // Folding, rotating and moving between displays keep the running activity.
  for (const kept of ['orientation', 'screenSize', 'smallestScreenSize', 'screenLayout', 'density', 'uiMode']) assert.ok(changes.includes(kept), kept);
});
