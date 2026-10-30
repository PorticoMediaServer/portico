/**
 * Android TV and Fire TV: the remote's Menu key opens the focused control's own menu and never
 * presses the control. It used to be sent as a held Select, which pressed every control that had
 * no long-press action (Sign out, Delete, Play) when the hold was released.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');

test('the activity never turns Menu into Select', () => {
  const activity = source('android/app/src/main/java/tv/getportico/portico/MainActivity.kt');
  assert.doesNotMatch(activity, /KEYCODE_DPAD_CENTER/);
  assert.doesNotMatch(activity, /postDelayed/);
  // The views still see the key, so JavaScript hears the remote's `menu` event.
  assert.match(activity, /window\.superDispatchKeyEvent\(event\)/);
});

// What Menu runs is covered behaviourally by next-controls-menu and next-overlay-keys.
test('the app installs the Menu key handler', () => {
  assert.match(source('App.tsx'), /useMenuKey\(\);/);
});
