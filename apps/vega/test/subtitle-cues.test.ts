/** The Vega player draws the server's cue document itself, by the rules every engine keeps. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {FOREIGN_ADDRESS, INVALID_COMMAND, NOTHING, captionStyleOf, decimalUs, displayAt, parseCueDocument, selectionOf, type SubtitleCommand} from '../src/bridge/subtitle-cues.ts';

const media = 'http://192.168.1.20:32500/v1/media/GRANT_abc/index.m3u8';
const command = (more: Partial<SubtitleCommand> = {}): SubtitleCommand => ({
  binding: {intentId: 3, sessionId: 's', generation: 2}, mode: 'track', revision: 4, resourceId: 'res_1', resourceRevision: 7, refreshRevision: 0, offsetUs: '0',
  documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/7', ...more,
});

test('a track is taken when its document is the session\'s own, over the scheme the media uses', () => {
  const selection = selectionOf(command({offsetUs: '-250000', style: {scale: 1.3, background: 'opaque'}}), media);
  assert.ok(!('error' in selection));
  assert.equal(selection.url, command().documentUrl);
  assert.equal(selection.offsetUs, -250000);
  assert.deepEqual(selection.style, {scale: 1.3, background: 'opaque', color: 'white', position: 'bottom', font: 'default'});
});

test('a document that is not this session\'s is refused', () => {
  const refused = (more: Partial<SubtitleCommand>, source = media) => assert.deepEqual(selectionOf(command(more), source), {error: FOREIGN_ADDRESS});
  refused({documentUrl: 'http://192.168.1.21:32500/v1/media/subtitles/GRANT_abc/res_1/7'}); // another host
  refused({documentUrl: 'http://192.168.1.20:8080/v1/media/subtitles/GRANT_abc/res_1/7'}); // another port
  refused({documentUrl: 'https://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/7'}); // another scheme
  refused({documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/OTHER/res_1/7'}); // another grant
  refused({documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/8'}); // another revision
  refused({documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_2/7'}); // another resource
  refused({documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/7?x=1'});
  refused({documentUrl: 'http://192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/7#x'});
  refused({documentUrl: 'http://user:pw@192.168.1.20:32500/v1/media/subtitles/GRANT_abc/res_1/7'});
  refused({documentUrl: 'file:///data/subtitles/a.vtt'});
  refused({}, 'http://192.168.1.20:32500/elsewhere/GRANT_abc/index.m3u8');
});

test('a command that does not say what it must is invalid', () => {
  for (const more of [{revision: 0}, {resourceRevision: 1.5}, {offsetUs: '00'}, {offsetUs: '700000000'}, {offsetUs: 12 as never}, {documentUrl: undefined}, {resourceId: undefined}] as Partial<SubtitleCommand>[]) {
    assert.deepEqual(selectionOf(command(more), media), {error: INVALID_COMMAND});
  }
});

test('microseconds are a decimal string in one spelling', () => {
  assert.equal(decimalUs('0', 10, true), 0);
  assert.equal(decimalUs('-5', 10, true), -5);
  assert.equal(decimalUs('-5', 10, false), null);
  assert.equal(decimalUs('05', 10, true), null);
  assert.equal(decimalUs('-0', 10, true), null);
  assert.equal(decimalUs('11', 10, true), null);
  assert.equal(decimalUs(5, 10, true), null);
});

test('the caption style is bounded and falls back to the default', () => {
  assert.deepEqual(captionStyleOf(undefined), {scale: 1, background: 'translucent', color: 'white', position: 'bottom', font: 'default'});
  assert.deepEqual(captionStyleOf({scale: 9, background: 'none'}), {scale: 3, background: 'none', color: 'white', position: 'bottom', font: 'default'});
  assert.deepEqual(captionStyleOf({scale: 0.1, background: 'purple'}), {scale: 0.5, background: 'translucent', color: 'white', position: 'bottom', font: 'default'});
});

const cueDocument = (cues: unknown[], more: object = {}) => JSON.stringify({version: 1, timeDomain: 'source-relative', cues, ...more});

test('the cue document is strict about its envelope and forgiving about cues', () => {
  assert.equal(parseCueDocument('not json'), null);
  assert.equal(parseCueDocument(cueDocument([], {version: 2})), null);
  assert.equal(parseCueDocument(cueDocument([], {timeDomain: 'wall'})), null);
  const list = parseCueDocument(cueDocument([
    {startUs: '1000000', endUs: '3000000', text: 'One'},
    {startUs: '2000000', endUs: '1000000', text: 'ends before it starts'},
    {startUs: '2500000', endUs: '4000000', text: '{\\an8}styled'},
    {startUs: 5000000, endUs: 6000000, text: 'Two', italic: true, top: true},
    {startUs: '4000000', endUs: '4500000', text: 'out of order'},
  ]));
  assert.ok(list);
  assert.deepEqual(list.cues.map(c => c.text), ['One', 'Two']);
  // Nothing readable at all in a document that has cues is a bad document.
  assert.equal(parseCueDocument(cueDocument([{startUs: 'x', endUs: 'y', text: 'z'}])), null);
  assert.deepEqual(parseCueDocument(cueDocument([]))?.cues, []);
});

test('the line on screen is every cue that covers the moment, with the track\'s offset', () => {
  const list = parseCueDocument(cueDocument([
    {startUs: '1000000', endUs: '9000000', text: 'Long'},
    {startUs: '2000000', endUs: '3000000', text: 'Short', italic: true},
    {startUs: '5000000', endUs: '6000000', text: 'Later', top: true},
  ]));
  assert.deepEqual(displayAt(list, 500000, 0), NOTHING);
  assert.deepEqual(displayAt(list, 2500000, 0), {text: 'Long\nShort', italic: true, top: false});
  // A long cue is still found behind shorter ones that began after it.
  assert.deepEqual(displayAt(list, 7000000, 0), {text: 'Long', italic: false, top: false});
  assert.deepEqual(displayAt(list, 5500000, 0), {text: 'Long\nLater', italic: false, top: true});
  // A positive offset shows every line later.
  assert.deepEqual(displayAt(list, 1500000, 1000000), NOTHING);
  assert.deepEqual(displayAt(list, 2500000, 1000000).text, 'Long');
  assert.deepEqual(displayAt(null, 2500000, 0), NOTHING);
});
