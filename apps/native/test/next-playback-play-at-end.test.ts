import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const runtime = readFileSync(new URL('../src-next/platform/playback/runtime.ts', import.meta.url), 'utf8');

// Structural guard (the runtime's toggle is an instance field that the offline harness cannot
// construct): #2945's "Play at the end of a film plays it again from its start" must not be lost
// to an older copy of runtime.ts.
test('Play at the end of a film is offered and replays the title through the player\'s own replay', () => {
  const toggle = /toggle = async \(fence: PlaybackFence\): Promise<void> => \{[\s\S]*?\n  \};/.exec(runtime)?.[0] ?? '';
  assert.match(toggle, /state\.phase === 'ended' && fence\.kind === 'queue'\) \{ await this\.replay\(fence\); return; \}/);
  assert.match(runtime, /play: ready && playback\.intent === 'paused' \|\| playback\.phase === 'ended' && fence\?\.kind === 'queue' && !this\.retired/);
});
