import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {canonicalMusicQualityLadder} from '../../contracts/src/types.ts';
import {parsePreferenceSnapshot, preferenceGroups, preferenceValue, validatePreferencePatch, withPreferencePatch, type PreferenceSnapshot, type PreferenceValue} from '../src/preferences.ts';
import {settingsBinding, readSettingsBinding, settingsBindingPatch, readingSettingsProperty, exactPresetValue, presetPatch, changedFromDefault} from '../src/presentation/settings-bindings.ts';
import {settingsRowIndex} from '../src/presentation/settings-spec.ts';

// How each settings row shows and saves a value, against `GET /v1/preferences` as the rebuilt server answers it.
const answer = JSON.parse(readFileSync(new URL('./fixtures/next-preferences.json', import.meta.url), 'utf8'));
const fresh = (): PreferenceSnapshot => parsePreferenceSnapshot(answer);
/** The settings with these values saved where the server keeps each one. */
function saved(values: Readonly<Record<string, PreferenceValue>> = {}, from = fresh()): PreferenceSnapshot {
  let out = from;
  for (const [key, value] of Object.entries(values)) out = withPreferencePatch(out, out.registry.fields.find(field => field.key === key)!.scopes[0]!, {[key]: value});
  return out;
}
const binding = (row: string) => {const found = settingsBinding(row); assert.ok(found, row); return found;};
const presetRow = (id: string) => settingsRowIndex.get(id)!.row.control.options!;

test('every row the server has a setting for shows the server\'s default, saved nowhere', () => {
  const preferences = fresh(), keys = new Set(preferences.registry.fields.map(field => field.key));
  let shown = 0;
  for (const {row} of settingsRowIndex.values()) {
    const mapped = settingsBinding(row.id);
    if (mapped?.form !== 'preferences' || !(mapped.fields ? Object.values(mapped.fields) : [mapped.key]).every(key => keys.has(key))) continue;
    const value = readSettingsBinding(mapped, {preferences}, row.id);
    assert.ok(value, row.id); assert.equal(value.source, 'default', row.id); assert.deepEqual(value.current, value.effective, row.id);
    shown++;
  }
  assert.ok(shown >= 50, `only ${shown} rows read`);
});

test('a setting kept for one device reads nothing, and saves nothing, where there is no device of its own', () => {
  const unbound = parsePreferenceSnapshot({...answer, deviceBound: false, documents: answer.documents.filter((document: {scope: string}) => document.scope !== 'profile-device')});
  for (const row of ['appearance.motion', 'quality.home', 'quality.directPlay']) {
    assert.equal(readSettingsBinding(binding(row), {preferences: unbound}), undefined, row);
    assert.throws(() => settingsBindingPatch(binding(row), row === 'appearance.motion' ? 'reduce' : row === 'quality.home' ? '4k' : 'prefer', {preferences: unbound}), row);
  }
  const preferences = saved({'appearance.motion': 'reduce', 'appearance.showBackdrops': false});
  assert.equal(readSettingsBinding(binding('appearance.motion'), {preferences})?.source, 'profile-device');
  assert.equal(readSettingsBinding(binding('appearance.backdrops'), {preferences})?.source, 'profile-device-class');
  assert.equal(readSettingsBinding(binding('appearance.backdrops'), {preferences})?.current, false);
  assert.throws(() => settingsBindingPatch(binding('appearance.textSize'), 'giant', {preferences}));
});

test('4K is shown for 2160p and saved as 2160p, with no other spelling either way', () => {
  for (const row of ['quality.home', 'quality.remote']) {
    const preferences = saved({[row]: '2160p'}), mapped = binding(row);
    assert.equal(readSettingsBinding(mapped, {preferences})?.current, '4k');
    assert.deepEqual(settingsBindingPatch(mapped, '4k', {preferences}), {form: 'preferences', values: {[row]: '2160p'}});
    for (const invalid of ['2160p', '900p', '', null, 4]) assert.throws(() => settingsBindingPatch(mapped, invalid, {preferences}), String(invalid));
    assert.equal(readSettingsBinding(mapped, {preferences: {...preferences, effective: {...preferences.effective, [row]: '4k'}}}), undefined);
  }
  // The owner's download ceiling uses the same choice.
  assert.deepEqual(settingsBindingPatch(binding('streaming.downloadQuality'), '4k'), {form: 'runtime', values: {downloadMaxQuality: '2160p'}});
  assert.throws(() => settingsBindingPatch(binding('streaming.downloadQuality'), 'automatic'));
});

test('Mark as watched at shows the server\'s default until the profile saves its own, and goes back to it', () => {
  const mapped = binding('playback.playedThresholdPercent'), inherited = fresh(), explicit = saved({'playback.playedThresholdPercent': 95});
  assert.deepEqual(readSettingsBinding(mapped, {preferences: inherited}), {current: 'server', effective: 'server', default: 'server', source: 'default', configOnly: false});
  const value = readSettingsBinding(mapped, {preferences: explicit})!;
  assert.equal(value.current, 95); assert.equal(value.default, 'server'); assert.equal(changedFromDefault(value.current, value.default), true);
  assert.deepEqual(settingsBindingPatch(mapped, 'server', {preferences: explicit}), {form: 'preferences', values: {'playback.playedThresholdPercent': null}});
  assert.deepEqual(settingsBindingPatch(mapped, null, {preferences: explicit}), {form: 'preferences', values: {'playback.playedThresholdPercent': null}});
  for (const invalid of ['95', 'default', 49, 101, 95.5, undefined]) assert.throws(() => settingsBindingPatch(mapped, invalid, {preferences: explicit}), String(invalid));
  // Saving the default takes the profile's own value away: the server's default applies again.
  assert.equal(readSettingsBinding(mapped, {preferences: withPreferencePatch(explicit, 'profile-server', {'playback.playedThresholdPercent': null})})?.current, 'server');
  // A value whose origin does not add up is not shown.
  assert.equal(readSettingsBinding(mapped, {preferences: {...inherited, documents: inherited.documents.filter(doc => doc.scope !== 'profile-server')}}), undefined);
  assert.equal(readSettingsBinding(mapped, {preferences: {...explicit, effectiveSource: {...explicit.effectiveSource, 'playback.playedThresholdPercent': 'default'}}}), undefined);
  for (const effective of ['95', true, 49, 101, 95.5]) assert.equal(readSettingsBinding(mapped, {preferences: {...inherited, effective: {...inherited.effective, 'playback.playedThresholdPercent': effective}}}), undefined);
  for (const change of [{type: 'boolean', default: true}, {default: 49}, {default: 95.5}]) {
    const preferences = {...inherited, registry: {...inherited.registry, fields: inherited.registry.fields.map(field => field.key === mapped.key ? {...field, ...change} : field)}} as PreferenceSnapshot;
    assert.equal(readSettingsBinding(mapped, {preferences}), undefined);
    if (change.type) assert.throws(() => settingsBindingPatch(mapped, 'server', {preferences}));
  }
});

test('the four presets read from the rows they set and save through them; Custom saves nothing', () => {
  const preferences = fresh(), device = {forceStereo: false, disableHdr: false};
  const read = (row: string) => readSettingsBinding(binding(row), {preferences, device})?.current;
  for (const [row, expected] of [['appearance.cardSize', 'medium'], ['privacy.level', 'normal'], ['quality.safeMode', 'automatic'], ['quality.playbackMethod', 'automatic']] as const) {
    const options = presetRow(row);
    assert.equal(exactPresetValue(options, read), expected, row);
    const reset = presetPatch(options, expected, read);
    assert.ok(Object.keys(reset).length > 0, row);
    const forms = new Set(Object.entries(reset).map(([target, value]) => settingsBindingPatch(binding(target), value, {preferences, device}).form));
    assert.equal(forms.size, 1, row);
    assert.throws(() => presetPatch(options, 'custom'), row);
  }
  // Playback method writes all three ways of delivering a title for this device.
  const original = presetPatch(presetRow('quality.playbackMethod'), 'original');
  assert.deepEqual(Object.fromEntries(Object.entries(original).map(([target, value]) => {const patch = settingsBindingPatch(binding(target), value, {preferences}); return [Object.keys(patch.values)[0], Object.values(patch.values)[0]];})),
    {'delivery.directPlay': 'require', 'delivery.directStream': 'never', 'delivery.transcode': 'never'});
  assert.equal(exactPresetValue(presetRow('quality.playbackMethod'), row => read(row) === undefined ? undefined : row === 'quality.transcode' ? 'require' : read(row)), 'custom');
  assert.equal(exactPresetValue(presetRow('appearance.cardSize'), () => 101), 'custom');
  assert.equal(exactPresetValue(presetRow('appearance.cardSize'), row => row === 'appearance.cardSizeExact' ? readSettingsBinding(binding(row), {preferences: saved({'appearance.cardSizePercent': 125})})?.current : undefined), 'large');
  // Device rows the presets also set.
  assert.deepEqual(settingsBindingPatch(binding('audioSubtitles.surround'), 'stereo').values, {forceStereo: true});
  assert.deepEqual(settingsBindingPatch(binding('device.player'), 'auto').values, {playerEngine: 'automatic'});
  assert.throws(() => settingsBindingPatch(binding('device.player'), 'automatic'));
  assert.deepEqual(settingsBindingPatch(binding('quality.tvSupports'), 'hdr-surround').values, {tvHdr: true, tvSurround: true});
  assert.equal(readSettingsBinding(binding('device.player'), {device: {playerEngine: 'invented'}}), undefined);
  assert.equal(readSettingsBinding(binding('quality.disableHdr'), {device: {disableHdr: 'false'}}), undefined);
  assert.throws(() => settingsBindingPatch(binding('quality.disableHdr'), 'false'));
  assert.equal(readSettingsBinding(binding('quality.disableHdr'), {device: {disableHdr: false}, deviceDefaults: {disableHdr: true}})?.default, true);
});

test('music quality shows CD for cd-quality and saves cd-quality, for this device only', () => {
  assert.deepEqual(canonicalMusicQualityLadder.map(q => q.id), ['original', 'cd-quality', '320', '96']);
  const defaults = fresh();
  for (const [row, expected] of [['music.qualityHome', 'original'], ['music.qualityAway', 'original'], ['music.qualityMobile', '320']] as const) {
    const mapped = binding(row);
    assert.equal(mapped.scope, 'profile-device'); assert.equal(mapped.key, row);
    const shown = readSettingsBinding(mapped, {preferences: defaults})!;
    assert.equal(shown.current, expected); assert.equal(shown.source, 'default');
    const preferences = saved({[row]: 'cd-quality'});
    assert.equal(readSettingsBinding(mapped, {preferences})?.current, 'cd');
    assert.equal(readSettingsBinding(mapped, {preferences})?.source, 'profile-device');
    assert.deepEqual(settingsBindingPatch(mapped, 'cd', {preferences}), {form: 'preferences', values: {[row]: 'cd-quality'}});
    for (const invalid of ['cd-quality', '256', '192', '128', 'automatic', 320]) assert.throws(() => settingsBindingPatch(mapped, invalid, {preferences}), String(invalid));
    assert.equal(readSettingsBinding(mapped, {preferences: {...preferences, effective: {...preferences.effective, [row]: '256'}}}), undefined);
    assert.equal(readSettingsBinding(mapped, {}), undefined);
  }
  for (const row of ['music.audioNormalization', 'music.crossfadeMs', 'music.gapless', 'music.neverBoost', 'music.smoothTransitions', 'music.dsdGainDb']) assert.equal(binding(row).scope, 'profile-server', row);
  assert.equal(settingsBinding('music.sampleRateMatching'), undefined);
  // The owner's away ceiling for music uses the same choice.
  const ceiling = binding('streaming.musicAwayQuality');
  assert.deepEqual(ceiling, {form: 'runtime', key: 'musicAwayQuality', codec: 'music-quality-choice'});
  assert.deepEqual(settingsBindingPatch(ceiling, 'cd'), {form: 'runtime', values: {musicAwayQuality: 'cd-quality'}});
  for (const invalid of ['cd-quality', '256', 'automatic', 320]) assert.throws(() => settingsBindingPatch(ceiling, invalid));
});

test('Skipping and Still watching each save their settings together; unknown rows have no binding', () => {
  const preferences = fresh(), skipping = binding('playback.skipping');
  assert.deepEqual(readSettingsBinding(skipping, {preferences})?.current, {intro: 'ask', credits: 'ask', recap: 'ask'});
  assert.deepEqual(settingsBindingPatch(skipping, {intro: 'auto', credits: 'off', recap: 'ask'}, {preferences}).values, {'playback.introSkip': 'auto', 'playback.creditsSkip': 'off', 'playback.recapSkip': 'ask'});
  assert.throws(() => settingsBindingPatch(skipping, {intro: 'auto', credits: 'off', unknown: 'ask'}, {preferences}));
  const still = binding('playback.stillWatching');
  assert.equal(readSettingsBinding(still, {preferences})?.current, 3);
  assert.deepEqual(settingsBindingPatch(still, 0, {preferences}).values, {'playback.passoutProtection': false});
  assert.deepEqual(settingsBindingPatch(still, 4, {preferences}).values, {'playback.passoutProtection': true, 'playback.passoutAfterEpisodes': 4});
  assert.throws(() => settingsBindingPatch(still, '4', {preferences}));
  for (const row of ['quality.playerEngine', 'audioSubtitles.burnIn', 'audioSubtitles.passthroughFormats', 'playback.introSkip', 'constructor', '__proto__', 'not.a.row']) assert.equal(settingsBinding(row), undefined, row);
  // Rewind on resume shows once the server publishes it, with the server's choices.
  assert.equal(readSettingsBinding(binding('playback.resumeRewindMs'), {preferences}), undefined);
  const rewind = parsePreferenceSnapshot({...answer, registry: {...answer.registry, fields: [...answer.registry.fields,
    {key: 'playback.resumeRewindMs', type: 'integer', default: 0, allowedValues: [0, 5000, 10000, 30000], scopes: ['profile-server'], group: 'playback', labelKey: 'preferences.playback.resumeRewindMs'}]}});
  assert.equal(readSettingsBinding(binding('playback.resumeRewindMs'), {preferences: rewind})?.current, 0);
  assert.deepEqual(settingsBindingPatch(binding('playback.resumeRewindMs'), 10000, {preferences: rewind}).values, {'playback.resumeRewindMs': 10000});
  assert.throws(() => settingsBindingPatch(binding('playback.resumeRewindMs'), 15000, {preferences: rewind}));
  // Reading rows go through the reading page's own settings, never a raw key.
  assert.equal(readingSettingsProperty('reading.theme'), 'theme');
  assert.equal(settingsBinding('reading.theme'), undefined);
  assert.equal(readingSettingsProperty('__proto__'), undefined);
});

test('the list is read in published order and a save is checked against it before it is sent', () => {
  const preferences = fresh(), registry = preferences.registry;
  assert.equal(preferenceValue<number>(preferences, 'playback.playedThresholdPercent'), 95);
  assert.throws(() => preferenceValue(preferences, 'playback.warpDrive'));
  assert.deepEqual(preferenceGroups(registry).map(group => group.group).slice(0, 3), ['playback', 'audiobooks', 'delivery']);
  assert.deepEqual(validatePreferencePatch(registry, 'profile-server', {'search.rememberHistory': false, 'playback.playedThresholdPercent': null}), {'search.rememberHistory': false, 'playback.playedThresholdPercent': null});
  for (const [scope, patch] of [['profile-server', {'playback.warpDrive': true}], ['profile-server', {'appearance.motion': 'reduce'}], ['profile-server', {'playback.defaultSpeed': 3.5}],
    ['profile-server', {'playback.playedThresholdPercent': 49}], ['profile-server', {'playback.playedThresholdPercent': 101}], ['profile-server', {'search.rememberHistory': 'yes'}]] as const)
    assert.throws(() => validatePreferencePatch(registry, scope, patch as never), JSON.stringify(patch));
  // A setting published twice keeps its first definition; one whose default is outside its own choices is left out.
  const twice = parsePreferenceSnapshot({...answer, registry: {...answer.registry, fields: [...answer.registry.fields, {...answer.registry.fields[0], default: 30000}]}});
  assert.equal(twice.registry.fields.filter(field => field.key === answer.registry.fields[0].key).length, 1);
  assert.equal(twice.registry.fields.find(field => field.key === answer.registry.fields[0].key)!.default, 10000);
  const outside = parsePreferenceSnapshot({...answer, registry: {...answer.registry, fields: answer.registry.fields.map((field: {key: string}) => field.key === 'playback.skipBackMs' ? {...field, default: 7000} : field)}});
  assert.equal(outside.registry.fields.some(field => field.key === 'playback.skipBackMs'), false);
  // Only an answer with no device class or no list is refused.
  for (const broken of [null, {...answer, deviceClass: 'watch'}, {...answer, registry: {}}]) assert.throws(() => parsePreferenceSnapshot(broken));
});
