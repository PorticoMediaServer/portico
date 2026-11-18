import assert from 'node:assert/strict';
import test from 'node:test';
import {settingsBinding,readSettingsBinding,settingsBindingPatch} from '../src/presentation/settings-bindings.ts';
import {parseMetadataSettings} from '../src/metadata-settings.ts';
import {hardwareSelectionChoices} from '../src/presentation/hardware-selection.ts';

const audience={scan:'both',storage:'owner',certificate:'both',recording:'off',feedback:'both'} as const;

test('Who sees each notice reads and writes the whole matrix, and nothing else',()=>{
 const binding=settingsBinding('general.noticeAudience')!;
 assert.deepEqual(binding,{form:'alerts',key:'noticeAudience',codec:'notice-audience'});
 assert.deepEqual(readSettingsBinding(binding,{forms:{alerts:{noticeAudience:audience}}})?.current,audience);
 assert.deepEqual(settingsBindingPatch(binding,{...audience,storage:'off'}),{form:'alerts',values:{noticeAudience:{...audience,storage:'off'}}});
 for(const invalid of [{...audience,storage:'everyone'},{scan:'both'},{...audience,backups:'both'},'both',null])assert.throws(()=>settingsBindingPatch(binding,invalid));
 assert.equal(readSettingsBinding(binding,{forms:{alerts:{}}}),undefined,'a server without routing offers no row');
});

test('Device offers Automatic, Processor only and each found adapter by its opaque id',()=>{
 const binding=settingsBinding('streaming.hardware')!;
 assert.deepEqual(binding,{form:'runtime',key:'hardwareSelection',codec:'hardware-selection'});
 assert.equal(readSettingsBinding(binding,{forms:{runtime:{hardwareSelection:'drm-0a1b'}}})?.current,'drm-0a1b');
 assert.deepEqual(settingsBindingPatch(binding,'software'),{form:'runtime',values:{hardwareSelection:'software'}});
 for(const invalid of ['/dev/dri/renderD128','vaapi\n','',12])assert.throws(()=>settingsBindingPatch(binding,invalid));
 assert.deepEqual(settingsBindingPatch(settingsBinding('streaming.hardwareDecode')!,false),{form:'runtime',values:{hardwareDecode:false}});
 const t=((id:string)=>id) as never;
 const choices=hardwareSelectionChoices([{id:'drm-0a1b',name:'Intel UHD 770',inUse:true}],'auto',t);
 assert.deepEqual(choices.map(c=>c.value),['auto','software','drm-0a1b']);
 const gone=hardwareSelectionChoices([],'drm-removed',t);
 assert.deepEqual(gone.at(-1),{value:'drm-removed',label:'next.settings.hardware.deviceGone',locked:true});
});

const metadata={revision:4,localArtworkLimitBytes:2**31,localArtworkUsedBytes:0,includeAdult:false,artworkMaxEdge:1920,tmdbCredentialConfigured:false,tvdbCredentialConfigured:false,
 openSubtitlesKeyConfigured:false,openSubtitlesUsername:'',openSubtitlesPasswordConfigured:false,openSubtitles:{keySource:'none',accountSource:'none',account:'none',downloadsRemaining:-1,downloadsAllowed:-1}};

test('metadata Advanced fields are read one by one, never carry the AcoustID key',async()=>{
 const advanced={refreshPolicy:'recent',fallbackLanguage:'original',animeTitles:'romaji',castPhotos:false,acoustidKeyConfigured:true,acoustidKeySource:'owner',refreshAll:{running:true,requestedAt:'2026-10-28T10:00:00Z'}};
 const parsed=parseMetadataSettings({...metadata,...advanced,acoustidKey:'secret'});
 assert.deepEqual(parsed.advanced,{...advanced});
 assert.equal(JSON.stringify(parsed).includes('secret'),false);
 const {refreshPolicy:_policy,...others}=advanced;
 assert.deepEqual(parseMetadataSettings({...metadata,...advanced,refreshPolicy:'weekly'}).advanced,others,'an unreadable field is left out, and only its row is not offered');
 assert.deepEqual(parseMetadataSettings({...metadata,castPhotos:true}).advanced,{castPhotos:true,acoustidKeySource:'none'},'a server with only cast photos offers that one row');
 assert.equal(parseMetadataSettings(metadata).advanced,undefined,'a server with none of them has no Advanced document');
});
