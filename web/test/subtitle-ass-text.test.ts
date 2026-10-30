import test from 'node:test';
import assert from 'node:assert/strict';
import {assPlainCues, plainCueLimits} from '../src/platform/playback/subtitles/ass-text.ts';
import {SubtitleSurface, type TextTrackLike} from '../src/platform/playback/subtitle-surface.ts';

const script = (...events: string[]) => ['﻿[Script Info]', 'PlayResX: 1920', '', '[V4+ Styles]', 'Format: Name, Fontname', 'Style: Default,Arial', '',
  '[Events]', 'Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text', ...events].join('\r\n');

test('"Styled subtitles" off: a styled script is read as its words and timing, as the server reads it', () => {
  const cues = assPlainCues(script(
    'Dialogue: 0,0:00:05.00,0:00:07.50,Default,,0,0,0,,{\\an8}A sign, up top',
    'Dialogue: 0,0:00:01.00,0:00:02.25,Default,,0,0,0,,{\\i1}Whispered,\\Nwith a comma{\\i0}',
    'Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,{\\p1}m 0 0 l 100 0 100 100 0 100{\\p0}',
    'Comment: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,not shown',
    'Dialogue: 0,0:00:08.00,0:00:09.00,Default,,0,0,0,,<b>bold</b>\\hspace &amp; more',
    'Dialogue: 0,0:00:08.00,0:00:09.00,Default,,0,0,0,,<b>bold</b>\\hspace &amp; more',
    'Dialogue: 0,0:00:10.00,0:00:09.00,Default,,0,0,0,,ends before it starts',
    'Dialogue: 0,0:00:11.00,0:00:12.00,Default,,0,0,0,,{\\b1}   ',
  ));
  assert.deepEqual(cues, [
    {startUs: 1_000_000, endUs: 2_250_000, text: 'Whispered,\nwith a comma', italic: true, top: false},
    {startUs: 5_000_000, endUs: 7_500_000, text: 'A sign, up top', italic: false, top: true},
    {startUs: 8_000_000, endUs: 9_000_000, text: 'bold space & more', italic: false, top: false},
  ], 'drawings, comments, duplicates, empty and backwards lines are left out; the rest are in time order');
});

test('the columns come from the script’s own Format line, and a script without lines is refused', () => {
  const cues = assPlainCues(['[Events]', 'Format: Start, End, Text', 'Dialogue: 0:00:01.5,0:00:02.05,commas, kept, in text'].join('\n'));
  assert.deepEqual(cues, [{startUs: 1_500_000, endUs: 2_050_000, text: 'commas, kept, in text', italic: false, top: false}]);
  assert.throws(() => assPlainCues(script()), /no lines/);
  assert.throws(() => assPlainCues('Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,outside Events'), /no lines/);
  assert.throws(() => assPlainCues(script('Dialogue: 0,0:61:00.00,0:62:00.00,Default,,0,0,0,,bad minutes')), /no lines/);
});

test('a line too long to be a subtitle keeps the part that fits, without splitting a character', () => {
  const [cue] = assPlainCues(script('Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,' + 'é'.repeat(plainCueLimits.cueBytes)));
  assert.equal(new TextEncoder().encode(cue!.text).length, plainCueLimits.cueBytes);
  assert.ok(!cue!.text.includes('�'));
});

test('the plain lines show on the text surface in the viewer’s style', () => {
  type Cue = {start: number; end: number; text: string; align: string; snapToLines: boolean; line: number | string};
  const shown: Cue[] = [];
  const track: TextTrackLike = {mode: 'disabled', get cues() { return this.mode === 'disabled' ? null : shown; }, addCue: c => { shown.push(c as Cue); }, removeCue: c => { shown.splice(shown.indexOf(c as Cue), 1); }};
  const surface = new SubtitleSurface({addTextTrack: () => track, textTracks: Object.assign([], {})}, {cue: (start, end, text) => ({start, end, text, align: '', snapToLines: false, line: 'auto'}), lift: false});
  surface.show(assPlainCues(script('Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\\an8\\i1}Sign')), 0.25);
  assert.deepEqual(shown.map(c => [c.start, c.end, c.text, c.line]), [[1.25, 2.25, '<i>Sign</i>', 0]]);
});
