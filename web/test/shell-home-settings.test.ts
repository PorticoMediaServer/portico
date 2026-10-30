import test from 'node:test';
import assert from 'node:assert/strict';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {shellPlaybackSettings, shellPlaybackSkips} from '../src/shell/settings.ts';

const preferences = {registry:{fields:[]},effective: {
  'playback.defaultSpeed': 1.25, 'audiobooks.defaultSpeed': 1.75,
  'playback.introSkip': 'auto', 'playback.creditsSkip': 'ask', 'playback.recapSkip': 'off',
  'music.gapless': true, 'music.crossfadeMs': 3000, 'music.audioNormalization': 'album',
  'music.shuffleDefault': true, 'music.repeatDefault': 'all',
  'quality.home': '2160p', 'quality.remote': '720p',
}} as unknown as PreferenceSnapshot;

test('the actual server resolves the network quality lane while video and audiobook rates remain distinct', () => {
  const settings = shellPlaybackSettings(preferences, {userAgent: 'Desktop', maxTouchPoints: 0});
  assert.equal(settings.defaultQuality, undefined);
  assert.equal(settings.videoSpeed(), 1.25); assert.equal(settings.audiobookSpeed(), 1.75);
  assert.deepEqual(settings.skipModes(), {intro: 'auto', credits: 'ask', recap: 'off'});
  assert.deepEqual(settings.audioEffects(), {neverBoost: false, smoothTransitions: true, gapless: true, crossfadeSeconds: 3, normalization: 'album'});
  assert.deepEqual(settings.musicDefaults?.(), {shuffle: true, repeat: 'all'});
  assert.equal(settings.webVolume(), true);
});
test('web volume is withheld on actual iPhone and desktop-mode iPad platforms', () => {
  assert.equal(shellPlaybackSettings(preferences, {userAgent: 'iPhone', maxTouchPoints: 5}).webVolume(), false);
  assert.equal(shellPlaybackSettings(preferences, {userAgent: 'Macintosh', maxTouchPoints: 5}).webVolume(), false);
  assert.equal(shellPlaybackSettings(preferences, {userAgent: 'Macintosh', maxTouchPoints: 0}).webVolume(), true);
});

test('the current audiobook uses its own two skip directions without changing video or music', () => {
  const selected = {...preferences, effective: {...preferences.effective,
    'playback.skipBackMs': 10000, 'playback.skipForwardMs': 30000,
    'audiobooks.skipBackMs': 60000, 'audiobooks.skipForwardMs': 90000,
    'audiobooks.resumeRewindMs': 5000}} as PreferenceSnapshot;
  assert.deepEqual(shellPlaybackSkips(selected, true), {back: 60, forward: 90});
  assert.deepEqual(shellPlaybackSkips(selected, false), {back: 10, forward: 30});
  assert.deepEqual(shellPlaybackSkips(preferences, true), {back: 15, forward: 30});
});


test('the shell subtitle callback reads the live confirmed preference source',()=>{
 let current={effective:{...preferences.effective,'playback.subtitleSize':'large','playback.subtitleColor':'white','playback.subtitleBackground':'outline','playback.subtitlePosition':'raised','playback.subtitleFont':'mono','playback.keepSubtitleStyling':false,'playback.useSystemCaptionStyle':false}} as unknown as PreferenceSnapshot;
 const settings=shellPlaybackSettings(()=>current,{userAgent:'Desktop',maxTouchPoints:0});
 assert.equal(settings.subtitlePreferences().color,'white');assert.equal(settings.subtitlePreferences().position,'raised');
 current={...current,effective:{...current.effective,'playback.subtitleColor':'yellow'}};
 assert.equal(settings.subtitlePreferences().color,'yellow');
});
