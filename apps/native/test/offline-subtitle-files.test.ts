/** The subtitle files kept beside a download: their names, which belong together, and the form a track is played from. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {storedSubtitle, subtitleExtraFiles, subtitleExtraName} from '../platform/offline-subtitle-files.ts';

const dir = 'file:///var/mobile/Containers/Data/Application/A/Library/Application%20Support/Downloads/dl1/extras/';
const file = (path: string) => ({path, contentType: 'application/octet-stream'});
const track = (files: Record<string, {path: string; contentType: string}>) => ({id: 's9', revision: 4, language: 'ja', title: 'Signs', default: false, forced: true, files});
const names = (list: string[]) => new Set(list);
/** The rule both native modules apply to a kept file's name. */
const SAFE = /^[A-Za-z0-9_-]{1,96}(\.[a-z0-9]{1,8})?$/;

test('every file of a track shares one stem; a VobSub index is kept as .idx beside its .sub', () => {
  const vobsub = track({vobsub: file('subtitles/s9/4/original'), sub: file('subtitles/s9/4/companion')});
  assert.deepEqual(subtitleExtraFiles(vobsub), [['sub-s9-4.idx', 'subtitles/s9/4/original'], ['sub-s9-4.sub', 'subtitles/s9/4/companion']]);
  // The player finds the pictures by putting "sub" in place of the index's three-letter extension.
  assert.equal(subtitleExtraName('s9', 4, 'vobsub').replace(/\.[a-z]{3}$/, '.sub'), subtitleExtraName('s9', 4, 'sub'));
  const styled = track({ass: file('subtitles/s9/4/original'), vtt: file('subtitles/s9/4.vtt'), font0: file('subtitles/s9/4/font-0'), font1: file('subtitles/s9/4/font-1')});
  assert.deepEqual(subtitleExtraFiles(styled).map(([name]) => name), ['sub-s9-4.ass', 'sub-s9-4.vtt', 'sub-s9-4.font0', 'sub-s9-4.font1']);
  for (const [name] of [...subtitleExtraFiles(vobsub), ...subtitleExtraFiles(styled), ...subtitleExtraFiles(track({dvb: file('x'), pgs: file('y'), font15: file('z')}))]) assert.match(name, SAFE);
});

test('a VobSub track is offered only once both of its files are on the device', () => {
  const vobsub = track({vobsub: file('a'), sub: file('b')});
  assert.equal(storedSubtitle(vobsub, names(['sub-s9-4.idx']), dir), undefined);
  assert.equal(storedSubtitle(vobsub, names(['sub-s9-4.sub']), dir), undefined);
  const whole = storedSubtitle(vobsub, names(['sub-s9-4.idx', 'sub-s9-4.sub']), dir)!;
  assert.equal(whole.format, 'vobsub');
  assert.equal(whole.url, dir + 'sub-s9-4.idx');
  assert.equal(whole.fonts, undefined);
  // An index kept under the old name cannot lead the player to its pictures: it is not offered.
  assert.equal(storedSubtitle(vobsub, names(['sub-s9-4.vobsub', 'sub-s9-4.sub']), dir), undefined);
});

test('a DVB track is offered from its own file', () => {
  const dvb = storedSubtitle(track({dvb: file('a')}), names(['sub-s9-4.dvb']), dir)!;
  assert.equal(dvb.format, 'dvb');
  assert.equal(dvb.url, dir + 'sub-s9-4.dvb');
});

test('a styled track comes with the fonts that have arrived, in their order, under the names they are kept by', () => {
  const styled = track({ass: file('a'), vtt: file('b'), font0: file('c'), font1: file('d'), font10: file('e'), font2: file('f')});
  const some = storedSubtitle(styled, names(['sub-s9-4.ass', 'sub-s9-4.vtt', 'sub-s9-4.font1', 'sub-s9-4.font10']), dir)!;
  assert.equal(some.format, 'ass');
  assert.equal(some.url, dir + 'sub-s9-4.ass');
  assert.equal(some.textUrl, dir + 'sub-s9-4.vtt', 'the saved plain reading remains available to the native renderer');
  assert.equal(some.revision, 4);
  assert.deepEqual(some.fonts, [{name: 'sub-s9-4.font1', url: dir + 'sub-s9-4.font1'}, {name: 'sub-s9-4.font10', url: dir + 'sub-s9-4.font10'}]);
  // Fonts never ride with the plain-text reading, and one the bundle does not list is never made up.
  const text = storedSubtitle(styled, names(['sub-s9-4.vtt', 'sub-s9-4.font0']), dir)!;
  assert.equal(text.format, 'vtt');
  assert.equal(text.fonts, undefined);
  assert.equal(storedSubtitle(track({ass: file('a')}), names(['sub-s9-4.ass', 'sub-s9-4.font0']), dir)!.fonts, undefined);
});

test('the form closest to how the track was authored wins; with nothing here the track is absent', () => {
  const both = track({pgs: file('a'), vtt: file('b')});
  assert.equal(storedSubtitle(both, names(['sub-s9-4.pgs', 'sub-s9-4.vtt']), dir)!.format, 'pgs');
  assert.equal(storedSubtitle(both, names(['sub-s9-4.vtt']), dir)!.format, 'vtt');
  assert.equal(storedSubtitle(both, names([]), dir), undefined);
  assert.equal(storedSubtitle(both, names(['sub-s9-4.pgs']), ''), undefined, 'no folder, no file');
});
