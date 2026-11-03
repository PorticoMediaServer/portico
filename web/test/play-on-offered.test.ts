import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {playOnOffered} from '../src/platform/playback/play-on.ts';

// AU-1: in a browser with neither Cast nor AirPlay (Firefox), Play on was shown for every queue run but its handler
// returned before opening, because the server's television list was only read by the panel it blocked.

test('Play on is offered only when it can do something', () => {
  const none = {castAvailable: false, airPlayAvailable: false, queueRun: true, receivers: 0};
  assert.equal(playOnOffered(none), false, 'neither transport and no television: not shown (desktop-player.html)');
  assert.equal(playOnOffered({...none, receivers: 1}), true, 'a Portico television the server lists');
  assert.equal(playOnOffered({...none, castAvailable: true}), true, 'Cast in Chrome or Edge');
  assert.equal(playOnOffered({...none, airPlayAvailable: true, queueRun: false}), true, 'AirPlay lifts any picture');
  assert.equal(playOnOffered({...none, castAvailable: true, receivers: 2, queueRun: false}), false, 'Cast and televisions take a queue run only');
});

test('the button and the panel use the same rule, and the list is read for the run, not by the panel', () => {
  const watch = readFileSync(new URL('../src/screens/watch/WatchPlayback.tsx', import.meta.url), 'utf8');
  assert.match(watch, /const castAvailable = !!cast && playOnOffered\(/);
  assert.match(watch, /useEffect\(\(\) => \{if \(cast && queueRun && !transport\) cast\.receivers\?\.refresh\(\);\}, \[cast, queueRun, transport\]\);/);
  const controller = readFileSync(new URL('../src/platform/playback/play-on.ts', import.meta.url), 'utf8');
  assert.match(controller, /if \(!playOnOffered\(\{castAvailable: this\.state\.cast\.available/);
  assert.doesNotMatch(watch, /!!cast\.receivers/, 'a receivers object alone no longer offers the button');
});
