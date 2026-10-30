/** Which player engine plays a title: the plan's word, the viewer's setting, and the local rule. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {chooseEngine, localRuleFacts, nativeEngineFeatures, parseEngineFeatures, parseEngineSetting, type EngineFacts} from '../src-next/platform/playback/engine-choice.ts';

const base: EngineFacts = {setting: 'automatic', mpvAvailable: true};

test('the plan names the engine, and that is the engine, whatever the setting', () => {
  assert.equal(chooseEngine({...base, planEngine: 'mpv'}), 'mpv');
  assert.equal(chooseEngine({...base, planEngine: 'native'}), 'native');
  assert.equal(chooseEngine({...base, setting: 'native', planEngine: 'mpv'}), 'mpv');
  assert.equal(chooseEngine({...base, setting: 'mpv', planEngine: 'native'}), 'native');
  assert.equal(chooseEngine({...base, setting: 'mpv', planEngine: 'vlc'}), 'mpv', 'a name this app does not know is no name');
});

test('a build without mpv, music and live channels always play in the native engine', () => {
  assert.equal(chooseEngine({...base, mpvAvailable: false, planEngine: 'mpv', setting: 'mpv'}), 'native');
  assert.equal(chooseEngine({...base, nativeOnly: true, planEngine: 'mpv', setting: 'mpv'}), 'native');
});

test('with no engine named, the setting decides, and Automatic follows the local rule', () => {
  assert.equal(chooseEngine({...base, setting: 'native', nativeNeeds: 'video-conversion'}), 'native');
  assert.equal(chooseEngine({...base, setting: 'mpv', nativeNeeds: 'nothing'}), 'mpv');
  assert.equal(chooseEngine(base), 'native', 'nothing known: the native player');
  assert.equal(chooseEngine({...base, nativeNeeds: 'nothing'}), 'native');
  for (const need of ['video-conversion', 'lossy-audio', 'burned-subtitles'] as const) {
    assert.equal(chooseEngine({...base, nativeNeeds: need}), 'mpv', need);
    assert.equal(chooseEngine({...base, nativeNeeds: need, mpvCanPlay: false}), 'native', `${need}, and mpv cannot play it either`);
  }
});

test('a subtitle track the device must draw in a kind the native engine does not draw needs mpv', () => {
  assert.equal(chooseEngine({...base, planEngine: 'native', requiredSubtitleRendering: 'pgs'}), 'mpv');
  assert.equal(chooseEngine({...base, requiredSubtitleRendering: 'ass', nativeSubtitleRendering: ['text']}), 'mpv');
  assert.equal(chooseEngine({...base, requiredSubtitleRendering: 'text'}), 'native');
  assert.equal(chooseEngine({...base, requiredSubtitleRendering: 'ass', nativeSubtitleRendering: ['text', 'ass']}), 'native');
  assert.equal(chooseEngine({...base, setting: 'native', requiredSubtitleRendering: 'pgs'}), 'native', 'the viewer chose the native player');
  assert.equal(chooseEngine({...base, mpvAvailable: false, requiredSubtitleRendering: 'pgs'}), 'native');
});

test('the local rule: a download the native player cannot open plays in mpv; a server stream was planned for the native player', () => {
  assert.deepEqual(localRuleFacts({address: 'file:///var/mobile/Downloads/a/film.mkv', local: true, platform: 'apple'}), {nativeNeeds: 'video-conversion', mpvCanPlay: true});
  assert.equal(localRuleFacts({address: 'file:///var/mobile/Downloads/a/film.MP4?x=1', local: true, platform: 'apple'}).nativeNeeds, 'nothing');
  assert.equal(localRuleFacts({address: 'file:///data/Downloads/a/film.mkv', local: true, platform: 'android'}).nativeNeeds, 'nothing', 'Media3 opens Matroska');
  assert.equal(localRuleFacts({address: 'file:///data/Downloads/a/film.avi', local: true, platform: 'android'}).nativeNeeds, 'video-conversion');
  assert.equal(localRuleFacts({address: 'file:///var/mobile/Downloads/a/film', local: true, platform: 'apple'}).nativeNeeds, 'nothing', 'no extension: nothing is known');
  assert.equal(localRuleFacts({address: 'https://portico.test/v1/media/grant/master.m3u8', local: false, platform: 'apple'}).nativeNeeds, 'nothing');
  assert.equal(chooseEngine({...base, ...localRuleFacts({address: 'file:///x/Downloads/film.mkv', local: true, platform: 'apple'})}), 'mpv');
});

test('the setting and the feature list read anything safely', () => {
  assert.equal(parseEngineSetting('mpv'), 'mpv');
  assert.equal(parseEngineSetting('native'), 'native');
  for (const junk of [undefined, null, '', 'MPV', 3, {}]) assert.equal(parseEngineSetting(junk), 'automatic');
  assert.deepEqual(parseEngineFeatures(undefined), nativeEngineFeatures);
  const mpv = parseEngineFeatures({pictureInPicture: false, airPlayVideo: false, liveChannels: false, musicSessions: false, audioTrackSwitching: true, subtitleRendering: ['text', 'ass', 'pgs', 'vobsub', 'dvb', 'smell'], chapters: true});
  assert.equal(mpv.pictureInPicture, false);
  assert.equal(mpv.airPlayVideo, false);
  assert.equal(mpv.audioTrackSwitching, true);
  assert.deepEqual(mpv.subtitleRendering, ['text', 'ass', 'pgs', 'vobsub', 'dvb']);
  assert.equal(parseEngineFeatures({pictureInPicture: 'no'}).pictureInPicture, true, 'a value of the wrong type falls back');
});
