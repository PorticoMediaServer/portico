import assert from 'node:assert/strict';
import test from 'node:test';
import {settingsBinding,readSettingsBinding,settingsBindingPatch,applySettingsBindingPatch} from '../src/presentation/settings-bindings.ts';

test('picture-size labels encode only the exact approved pixel edges',()=>{
 const binding=settingsBinding('metadata.artworkSize')!;
 assert.deepEqual(binding,{form:'metadata',key:'artworkMaxEdge',codec:'artwork-size'});
 for(const [choice,edge]of [['small',1280],['standard',1920],['large',2560]] as const){
  assert.deepEqual(settingsBindingPatch(binding,choice),{form:'metadata',values:{artworkMaxEdge:edge}});
  assert.equal(readSettingsBinding(binding,{forms:{metadata:{artworkMaxEdge:edge}}})?.current,choice);
 }
 for(const invalid of ['1280','1920','2560',1280,1920,2560,'custom','larger',null,true])assert.throws(()=>settingsBindingPatch(binding,invalid));
 for(const invalid of [undefined,1281,1921,2561,'1920',null])assert.equal(readSettingsBinding(binding,{forms:{metadata:{artworkMaxEdge:invalid}}}),undefined);
});

test('picture-size projection retains actual current/effective/default and file source',()=>{
 const binding=settingsBinding('metadata.artworkSize')!;
 const configuration={revision:9,registryRevision:'settings',path:'/config/portico.toml',fields:[{id:'metadata.artworkSize',key:'metadata.artwork_max_edge',current:1280,effective:1920,default:2560,source:'file' as const,configOnly:false,applicationMode:'next-operation'}]};
 assert.deepEqual(readSettingsBinding(binding,{configuration},'metadata.artworkSize'),{current:'small',effective:'standard',default:'large',source:'file',configOnly:false,applicationMode:'next-operation'});
 // A captured form alone never manufactures a factory default or provenance.
 assert.equal(readSettingsBinding(binding,{forms:{metadata:{artworkMaxEdge:1280}}})?.default,undefined);
 const original={revision:7,localArtworkLimitBytes:4096,includeAdult:false,artworkMaxEdge:1280};
 assert.deepEqual(applySettingsBindingPatch(original,settingsBindingPatch(binding,'large')),{...original,artworkMaxEdge:2560});
 assert.equal(original.artworkMaxEdge,1280);
});
