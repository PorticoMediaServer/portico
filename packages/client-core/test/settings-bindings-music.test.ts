import test from 'node:test';
import assert from 'node:assert/strict';
import {canonicalMusicQualityLadder} from '../../contracts/src/types.ts';
import {parsePreferenceSnapshot,type PreferenceScope,type PreferenceValue} from '../src/preferences.ts';
import {settingsBinding,readSettingsBinding,settingsBindingPatch} from '../src/presentation/settings-bindings.ts';

// Published Music registry inputs, never application defaults or runtime facts.
function snapshot(values:Record<string,PreferenceValue>={}){
 const qualities=['music.qualityHome','music.qualityAway','music.qualityMobile','music.qualityDownloads'];
 const fields=qualities.map(key=>({key,type:'string',default:key==='music.qualityHome'?'original':'320',allowedValues:canonicalMusicQualityLadder.map(q=>q.id),scopes:['profile-device-class'],group:'music',labelKey:'preferences.'+key}));
 return parsePreferenceSnapshot({deviceClass:'web',registryRevision:'music.2',registry:{revision:'music.2',fields},documents:[{scope:'profile-server',revision:7,digest:'b'.repeat(64),activeRevision:7,values:{}},{scope:'profile-device-class',revision:7,digest:'a'.repeat(64),activeRevision:7,values}],effective:Object.fromEntries(fields.map(f=>[f.key,values[f.key]??f.default])),effectiveSource:Object.fromEntries(fields.map(f=>[f.key,Object.hasOwn(values,f.key)?'profile-device-class':'default'])),clampedFields:[]});
}
test('drawn CD choices write and read the canonical four-rung Music registry',()=>{
 assert.deepEqual(canonicalMusicQualityLadder.map(q=>q.id),['original','cd-quality','320','96']);
 for(const row of ['music.qualityHome','music.qualityAway','music.qualityMobile','music.downloadQuality']){
  const binding=settingsBinding(row)!;assert.equal(binding.scope,'profile-device-class');
  assert.equal(binding.key,row==='music.downloadQuality'?'music.qualityDownloads':row);
  const preferences=snapshot({[binding.key]:'cd-quality'});
  assert.equal(readSettingsBinding(binding,{preferences})?.current,'cd');
  assert.equal(readSettingsBinding(binding,{preferences})?.source,'profile-device-class');
  assert.deepEqual(settingsBindingPatch(binding,'cd',{preferences}),{form:'preferences',values:{[binding.key]:'cd-quality'}});
  for(const invalid of ['cd-quality','256','192','128','automatic',320])assert.throws(()=>settingsBindingPatch(binding,invalid,{preferences}));
  assert.equal(readSettingsBinding(binding,{preferences:{...preferences,effective:{...preferences.effective,[binding.key]:'256'}}}),undefined);
  assert.equal(readSettingsBinding(binding,{}),undefined);
 }
});
test('Music defaults come from the real registry document without changing scopes',()=>{
 const preferences=snapshot();
 for(const row of ['music.qualityHome','music.qualityAway','music.qualityMobile','music.downloadQuality']){
  const value=readSettingsBinding(settingsBinding(row)!,{preferences})!;
  assert.equal(value.current,row==='music.qualityHome'?'original':'320');assert.equal(value.source,'default');
 }
 for(const row of ['music.audioNormalization','music.crossfadeMs','music.gapless','music.neverBoost','music.smoothTransitions','music.dsdGainDb'])assert.equal(settingsBinding(row)?.scope,'profile-server' satisfies PreferenceScope);
 // Local sample-rate policy has no account registry producer here.
 assert.equal(settingsBinding('music.sampleRateMatching'),undefined);
});

test('owner away Music ceiling shares the exact four-rung codec and real configuration sources',()=>{
 const binding=settingsBinding('streaming.musicAwayQuality')!;
 assert.deepEqual(binding,{form:'runtime',key:'musicAwayQuality',codec:'music-quality-choice'});
 const configuration={revision:3,registryRevision:'settings-r40.1',path:'/state/portico.toml',fields:[{id:'streaming.musicAwayQuality',key:'streaming.music_away_quality',current:'cd-quality',effective:'96',default:'original',source:'file' as const,configOnly:false,applicationMode:'next-operation'}]};
 assert.deepEqual(readSettingsBinding(binding,{configuration},'streaming.musicAwayQuality'),{current:'cd',effective:'96',default:'original',source:'file',configOnly:false,applicationMode:'next-operation'});
 assert.deepEqual(settingsBindingPatch(binding,'cd'),{form:'runtime',values:{musicAwayQuality:'cd-quality'}});
 for(const invalid of ['cd-quality','256','192','128','automatic',320])assert.throws(()=>settingsBindingPatch(binding,invalid));
});
