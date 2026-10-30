import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createLandingStore} from '../src-next/screens/watch/landing-store.ts';

test('P2-71-PERF: a held scrub re-renders only what prints the landing time, the chrome flips twice', () => {
  const store = createLandingStore();
  let hintRenders = 0, chromeRenders = 0, scrubbing = false;
  store.subscribe(() => hintRenders++);
  // The chrome's own state: set only when "scrubbing" changes, as React bails out on an equal value.
  const setLanding = (seconds: number | undefined) => {store.set(seconds); const next = seconds !== undefined; if (next !== scrubbing) {scrubbing = next; chromeRenders++;}};
  for (let step = 1; step <= 40; step++) setLanding(100 + step * 10);
  setLanding(undefined);
  assert.equal(hintRenders, 41, 'the hint follows every step and the end');
  assert.equal(chromeRenders, 2, 'the 800-line chrome renders on start and end only');
  const chrome = readFileSync(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(chrome, /useState<number>\(\), \[returnAt/, 'no per-step landing state in the chrome');
  assert.match(chrome, /<LandingTime store=\{landingStore\} fallback=\{returnAt\}\/>/);
});

test('P2-71-PERF: a panel scroll is remembered without publishing to every chrome subscriber', () => {
  const controls = readFileSync(new URL('../src-next/screens/watch/controls.ts', import.meta.url), 'utf8');
  const remember = controls.slice(controls.indexOf('  rememberPanel('), controls.indexOf('  closePanel('));
  assert.doesNotMatch(remember, /this\.publish\(/);
});
