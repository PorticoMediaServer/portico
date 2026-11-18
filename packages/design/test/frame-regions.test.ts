import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {frameRegions} from '../src/frame-regions.ts';

// Both apps' player frames take exactly the regions the data names (Stage 1 round 8): each region id is a prop of the
// frame's props type in the native and the web frame files.
const files = {
  player: ['../../../apps/native/src-next/kit/frames/PlayerChromeFrame.tsx', '../../../web/src/kit/frames/PlayerFrames.tsx'],
  audio: ['../../../apps/native/src-next/kit/frames/AudioPlayerFrame.tsx', '../../../web/src/kit/frames/PlayerFrames.tsx'],
} as const;
const props = {player: 'PlayerChromeFrameProps', audio: 'AudioPlayerFrameProps'} as const;

for (const key of ['player', 'audio'] as const) test(`every ${key} region is a prop of both apps' frame`, () => {
  for (const file of files[key]) {
    const source = readFileSync(new URL(file, import.meta.url), 'utf8');
    const start = source.indexOf(`export type ${props[key]}`), end = source.indexOf('\n}>;', start);
    assert.ok(start >= 0, `${props[key]} in ${file}`);
    const block = source.slice(start, end);
    const web = file.startsWith('../../../web');
    for (const region of frameRegions[key]) {
      // A region the web never draws (TV hints, the TV playlist button) is not a web prop.
      if (web && !region.forms.some(form => form !== 'tv')) continue;
      // A region only the web draws (the audio player's volume) is not a native prop.
      if (!web && region.forms.every(form => form === 'web')) continue;
      assert.match(block, new RegExp(`\\b${region.id}\\??:`), `${region.id} in ${file}`);
    }
  }
});
