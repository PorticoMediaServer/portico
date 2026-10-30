import test from 'node:test';
import assert from 'node:assert/strict';
import {createWindowChromePublisher, getWindowChromeEnvironment, installWindowChromeHost, subscribeWindowChromeEnvironment, type WindowChromeState} from '../src/platform/window-chrome.ts';

test('page scroll and actual fullscreen are delivered to a host installed after the page', () => {
  const page = createWindowChromePublisher('pages'), states: WindowChromeState[] = [];
  page.update({controlsVisible: true, fullscreen: false, scrolled: true});
  const installation = installWindowChromeHost({update: state => states.push(state)});
  try {
    assert.deepEqual(states, [{surface: 'pages', controlsVisible: true, fullscreen: false, scrolled: true}]);
    assert.equal(getWindowChromeEnvironment(), undefined);
    installation.updateEnvironment({os: 'windows', fullscreen: true});
    assert.equal(states.at(-1)?.fullscreen, true);
    page.update({controlsVisible: true, fullscreen: true, scrolled: false});
    installation.updateEnvironment({os: 'windows', fullscreen: false});
    assert.equal(states.at(-1)?.fullscreen, true, 'DOM fullscreen survives leaving native fullscreen');
    page.update({controlsVisible: true, fullscreen: false, scrolled: false});
    assert.equal(states.at(-1)?.fullscreen, false);
  } finally { page.dispose(); installation.dispose(); }
});

test('hidden media controls retain the media surface and disposal restores the current page state', () => {
  const states: WindowChromeState[] = [], installation = installWindowChromeHost({update: state => states.push(state)});
  const page = createWindowChromePublisher('pages'), video = createWindowChromePublisher('video');
  try {
    page.update({controlsVisible: true, fullscreen: false, scrolled: false});
    assert.equal(states.at(-1)?.surface, 'pages', 'creating a publisher does not pretend a surface is visible');
    video.update({controlsVisible: true, fullscreen: false});
    video.update({controlsVisible: false, fullscreen: false});
    page.update({controlsVisible: true, fullscreen: false, scrolled: true});
    assert.deepEqual(states.at(-1), {surface: 'video', controlsVisible: false, fullscreen: false});
    video.dispose();
    assert.deepEqual(states.at(-1), {surface: 'pages', controlsVisible: true, fullscreen: false, scrolled: true});
    const count = states.length;
    video.update({controlsVisible: true, fullscreen: true});
    video.dispose();
    assert.equal(states.length, count, 'late media events cannot restore a closed surface');
  } finally { video.dispose(); page.dispose(); installation.dispose(); }
});

test('a newly mounted music surface supersedes video until it closes', () => {
  const states: WindowChromeState[] = [], installation = installWindowChromeHost({update: state => states.push(state)});
  const video = createWindowChromePublisher('video'), music = createWindowChromePublisher('music');
  try {
    video.update({controlsVisible: true, fullscreen: false});
    music.update({controlsVisible: true, fullscreen: false});
    video.update({controlsVisible: false, fullscreen: false});
    assert.equal(states.at(-1)?.surface, 'music');
    music.dispose();
    assert.deepEqual(states.at(-1), {surface: 'video', controlsVisible: false, fullscreen: false});
  } finally { music.dispose(); video.dispose(); installation.dispose(); }
});

test('replacement retires the old host once and rejects its late native facts and cleanup', () => {
  const first: WindowChromeState[] = [], second: WindowChromeState[] = [];
  let disposed = 0, notifications = 0;
  const off = subscribeWindowChromeEnvironment(() => notifications++);
  const old = installWindowChromeHost({update: state => first.push(state), dispose: () => disposed++});
  old.updateEnvironment({os: 'macos', fullscreen: false});
  const current = installWindowChromeHost({update: state => second.push(state)});
  try {
    assert.equal(disposed, 1);
    current.updateEnvironment({os: 'linux', fullscreen: false});
    const count = second.length, changes = notifications;
    old.updateEnvironment({os: 'windows', fullscreen: true});
    old.dispose();
    assert.equal(disposed, 1);
    assert.equal(second.length, count);
    assert.equal(notifications, changes);
    assert.deepEqual(getWindowChromeEnvironment(), {os: 'linux', fullscreen: false});
    current.updateEnvironment({os: 'linux', fullscreen: false});
    assert.equal(notifications, changes, 'identical native facts do not invalidate the shell');
    current.dispose();
    assert.equal(getWindowChromeEnvironment(), undefined);
    assert.equal(notifications, changes + 1);
  } finally { off(); old.dispose(); current.dispose(); }
});
