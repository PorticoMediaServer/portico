import test from 'node:test';
import assert from 'node:assert/strict';
import {videoPresentationLayout, VideoPresentationContinuity, type PlaybackWindow} from '../src/platform/playback/video-presentation.ts';

test('Q7 flat presentation follows the real window shape, including the accepted faint crease', () => {
  const wide = videoPresentationLayout({width: 890, height: 626, posture: 'flat'}, false);
  assert.deepEqual(wide, {mode: 'full-window', hideStatusAndRail: true, panelWidth: 380, picture: {x: 0, y: 62.6875, width: 890, height: 500.625}});
  const tall = videoPresentationLayout({width: 840, height: 880, posture: 'flat'}, false);
  assert.equal(tall.mode, 'watch'); assert.equal(tall.hideStatusAndRail, false);
  assert.deepEqual(tall.picture, {x: 0, y: 0, width: 840, height: 472.5});
  const crease = {axis: 'vertical' as const, x: 440, y: 0, width: 10, height: 626};
  assert.deepEqual(videoPresentationLayout({width: 890, height: 626, posture: 'flat', hinge: crease}, false).picture, wide.picture);
  assert.equal(videoPresentationLayout({width: 890, height: 626, posture: 'flat', hinge: crease}, false).panelWidth, 440, 'the panel uses the actual right page');
});

test('Q7 book and tabletop keep the picture out of the actual hinge clearance; docked stays docked', () => {
  const book: PlaybackWindow = {width: 890, height: 626, posture: 'book', hinge: {axis: 'vertical', x: 440, y: 0, width: 10, height: 626}};
  const left = videoPresentationLayout(book, false);
  assert.equal(left.mode, 'book'); assert.equal(left.picture.x + left.picture.width, left.hingeBand!.x);
  assert.equal(left.picture.width, 424); assert.equal(left.panelWidth, 440);
  const tabletop: PlaybackWindow = {width: 840, height: 880, posture: 'tabletop', hinge: {axis: 'horizontal', x: 0, y: 430, width: 840, height: 20}};
  const top = videoPresentationLayout(tabletop, false);
  assert.equal(top.mode, 'tabletop'); assert.ok(top.picture.y + top.picture.height <= top.hingeBand!.y);
  assert.equal(videoPresentationLayout(book, true).mode, 'docked');
  assert.throws(() => videoPresentationLayout({...book, hinge: undefined}, false), /playback_hinge_unavailable/);
  assert.throws(() => videoPresentationLayout({...tabletop, hinge: book.hinge}, false), /playback_hinge_unavailable/);
  assert.throws(() => videoPresentationLayout({...book, hinge: {...book.hinge!, x: 900}}, false), /invalid_playback_hinge/);
});

test('Q7 one settled transition gets one fade and a three-second controls reveal, with reduced motion honored', () => {
  const continuity = new VideoPresentationContinuity(), events: unknown[][] = [];
  const effects = {layout: (value: unknown) => events.push(['layout', value]), crossfade: (ms: number) => events.push(['fade', ms]), revealControls: (ms: number) => events.push(['controls', ms])};
  const closed: PlaybackWindow = {width: 390, height: 844, posture: 'closed'};
  const flat: PlaybackWindow = {width: 890, height: 626, posture: 'flat'};
  continuity.apply(closed, false, false, effects); continuity.apply(closed, false, false, effects);
  assert.equal(events.length, 1);
  continuity.apply(flat, false, false, effects); continuity.apply(flat, false, false, effects);
  assert.deepEqual(events.slice(-2), [['fade', 250], ['controls', 3000]]); assert.equal(events.length, 4);
  continuity.apply(closed, false, true, effects);
  assert.deepEqual(events.slice(-2), [['fade', 0], ['controls', 3000]]);
  continuity.apply(flat, true, false, effects);
  assert.equal(events.at(-1)![0], 'fade', 'a docked video does not reveal expanded controls');
});
