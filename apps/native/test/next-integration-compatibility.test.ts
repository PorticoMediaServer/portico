import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks,screenModule} from './helpers/screen-harness.mjs';

test('TV and non-owner Settings never start app-password or secret requests',async()=>{
 for(const mode of [{tv:true,owner:true},{tv:false,owner:false}]){
  const h=hooks();let constructed=0;
  class DeniedSource{constructor(){constructed++;throw new Error('Shared or non-owner surface created a credential source');}}
  const module=await screenModule(new URL('../src-next/screens/settings-live/server/integrations/AppPasswords.tsx',import.meta.url),{
   react:{...h.react,useLayoutEffect:(setup:()=>unknown)=>{setup();}},
   'react-native':{Platform:{isTV:mode.tv},Text:'Text',View:'View'},
   '../../../../../../../packages/client-core/src/server-admin/integrations-source':{SettingsIntegrationsSource:DeniedSource},
   '../../../../../../../packages/i18n/src':{defaultI18n:{}},
   '../../../../kit/controls':{Button:'Button',Select:'Select',Switch:'Switch',TextField:'TextField'},
   '../../../../kit/overlays':{BottomSheet:'BottomSheet'},'../../../../kit/theme':{useTheme:()=>({})},
   '../../../../kit/shell/visual':{shellType:()=>({})},'../../../../platform/clipboard':{copyText:()=>{throw new Error('No copy on shared screens');}},
   '../../lifetime':{NativeScreenLifetime:DeniedSource,screenScopeEnded:()=>new Error('retired')},
   '../storage/space-source':{nativeStorageOwner:()=>mode.owner},
  });
  const tree=h.render(()=>module.NativeAppPasswords({ports:{context:{},requestId:()=> 'request'},runtime:{reportError(){}}}));
  assert.equal(tree,null);assert.equal(constructed,0);
 }
});
