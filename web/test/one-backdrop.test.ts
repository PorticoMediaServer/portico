import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';

// Owner, 17 and 18 Nov (round 8, R10): ONE backdrop everywhere. `body` draws it in kit/backdrop.css and nothing else
// does. Only the files below, each of which must hide the page beneath it, may stand on the one cover class, and only
// the two audio players draw the artwork glow. A page that wants a backdrop of its own is asking for a fix in its
// host or its frame, not for a line here.
const root = new URL('../src/', import.meta.url);
const OWNER = 'kit/backdrop.css';
const COVERS: Record<string, string> = {
  'shell/Boot.tsx': 'the wait while a server opens, over the app until it fades',
  'kit/states/State.tsx': 'a player state, over the picture',
  'screens/watch/CapacityWait.tsx': 'the wait before playback starts',
  'screens/watch/music/NowPlaying.tsx': 'the audio player (artwork glow)',
  'screens/watch/listening/AudiobookListening.tsx': 'the audiobook player (artwork glow)',
  'screens/dashboard/phone.tsx': 'the owner dashboard as a whole phone page',
  'dev/entries/live-player.tsx': 'the live player specimen, standing in for a picture',
  'dev/families/artwork.tsx': 'the Kit gallery case that shows the backdrop',
};
const GLOW = ['kit/artwork/AmbientArt.tsx', 'kit/artwork/artwork-glow.module.css', 'kit/artwork/index.ts', 'screens/watch/music/NowPlaying.tsx', 'screens/watch/listening/AudiobookListening.tsx'];

function sources(dir = ''): string[] {
  return readdirSync(new URL(dir, root), {withFileTypes: true}).flatMap(entry => entry.isDirectory() ? sources(`${dir}${entry.name}/`)
    : /\.(tsx?|css)$/.test(entry.name) ? [`${dir}${entry.name}`] : []);
}

test('only body draws the backdrop, and only named covers stand on it', () => {
  const covers: string[] = [], glows: string[] = [];
  for (const file of sources()) {
    if (file === OWNER) continue;
    const text = readFileSync(new URL(file, root), 'utf8');
    assert.doesNotMatch(text, /ColourBackdrop|portico-backdrop|--backdrop-glows/, `${file} draws a backdrop of its own`);
    if (/portico-cover/.test(text)) covers.push(file);
    if (/\bArtworkGlow\b|artwork-glow\.module/.test(text)) glows.push(file);
  }
  assert.deepEqual(covers.sort(), Object.keys(COVERS).sort());
  assert.deepEqual(glows.sort(), [...GLOW].sort());
});
