import test from 'node:test';
import assert from 'node:assert/strict';
import {forcedImageLinesPath} from '../src-next/platform/playback/forced-image.ts';
import type {SubtitleRender} from '../src-next/platform/playback/engine-media.ts';

const media = 'https://portico.test/v1/media/current/file';
const resource = {id: 'subtitle', revision: 3};
const image: SubtitleRender = {kind: 'pgs', url: 'https://portico.test/v1/media/subtitles/current/subtitle/3/original.sup'};
const lines = '/v1/media/subtitles/current/subtitle/3/lines';

test('only the actually offered lines of the current image/resource/grant can be read', () => {
  assert.equal(forcedImageLinesPath(media, image, resource, lines), lines);
  assert.equal(forcedImageLinesPath(media, {kind: 'vobsub', url: image.url!.replace('.sup', '.idx')}, resource, lines), lines);
  assert.equal(forcedImageLinesPath(media, image, resource, undefined), undefined);
});

test('foreign origins, grants, resources, revisions and unrelated formats never grant a count read', () => {
  for (const render of [
    {...image, url: image.url!.replace('portico.test', 'other.test')},
    {...image, url: image.url!.replace('/current/', '/other/')},
    {...image, url: image.url!.replace('/subtitle/', '/foreign/')},
    {...image, url: image.url!.replace('/3/', '/4/')},
    {...image, kind: 'ass' as const}, {...image, kind: 'text' as const},
    {...image, url: image.url!.replace('.sup', '.idx')}, {kind: 'pgs' as const},
  ]) assert.equal(forcedImageLinesPath(media, render, resource, lines), undefined);
  for (const offered of [lines + '?anything', lines + '#fragment', lines.replace('/3/', '/4/'), lines.replace('/current/', '/other/'), 'https://other.test' + lines]) {
    assert.equal(forcedImageLinesPath(media, image, resource, offered), undefined);
  }
});

test('ambiguous or credential-bearing addresses and unknown resource facts remain unavailable', () => {
  for (const url of [image.url! + '?x', image.url! + '#x', image.url!.replace('https://', 'https://user:pass@'),
    image.url!.replace('/subtitle/', '/x/../subtitle/'), image.url!.replace('/subtitle/', '/%73ubtitle/'), image.url! + '\n']) {
    assert.equal(forcedImageLinesPath(media, {...image, url}, resource, lines), undefined);
  }
  assert.equal(forcedImageLinesPath('file:///download/movie', image, resource, lines), undefined);
  assert.equal(forcedImageLinesPath('/v1/media/current/file', image, resource, lines), undefined, 'the runtime must supply its actual resolved media route');
  assert.equal(forcedImageLinesPath(media, image, undefined, lines), undefined);
  assert.equal(forcedImageLinesPath(media, image, {...resource, revision: Number.NaN}, lines), undefined);
});
