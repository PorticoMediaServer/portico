import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = readFileSync(new URL('../src-next/shell/TouchStacks.tsx', import.meta.url), 'utf8');

test('M18: reduced motion cross-fades pushes, pops and tab switches itself, so a zero animator scale never turns them into a cut', () => {
  // The platform's push is only used in the full tier; reduced and lite take no platform animation.
  assert.match(source, /tier === 'full' \? 'default' : 'none'/);
  assert.match(source, /export const NATIVE_PUSH_FADE_MS = 160;/);
  assert.match(source, /tier === 'full' \? 120 : tier === 'reduced' \? 160 : 0/);
  // Reanimated's own clock, explicitly not skipped by the system Reduce Motion setting.
  assert.match(source, /withTiming\(1, \{duration: ms, easing: Easing\.linear, reduceMotion: ReduceMotion\.Never\}\)/);
  // Push and pop alike: the page that becomes this stack's top fades in; an iOS edge swipe doesn't fade again.
  assert.match(source, /tier === 'reduced' \? <FadeIn run=\{index === presentations\.length - 1\} appear=\{index > 0\} ms=\{swiped\.current \? 0 : NATIVE_PUSH_FADE_MS\}>/);
  assert.match(source, /if \(Platform\.OS === 'ios'\) swiped\.current = true;/);
  assert.match(source, /if \(was === false\) opacity\.value = 0;/);
  assert.match(source, /<FadeIn run=\{active\} ms=\{nativeTabFadeMs\(tier\)\}>/);
});
