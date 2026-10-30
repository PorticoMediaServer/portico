import {SettingsLibraryChannelsSource} from '../../../../../../../packages/client-core/src/server-admin/channels-source';
import {SettingsChannelEditorSource} from '../../../../../../../packages/client-core/src/server-admin/channel-editor-source';
import {SettingsChannelActionsSource} from '../../../../../../../packages/client-core/src/server-admin/channel-actions-source';
import {ChannelFieldsSource} from '../../../../../../../packages/client-core/src/server-admin/channel-fields-source';
import {SettingsChannelLogoSource} from '../../../../../../../packages/client-core/src/server-admin/channel-logo-source';
import {nativeChannelLogoUpload,nativeChannelLogoCapability,type NativeLogoImage} from './logo-upload';
import {BlockPresetsSource} from '../../../../../../../packages/client-core/src/server-admin/block-presets-source';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {nativeRouteCrypto} from '../../../../platform/server-connections';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
export const nativeChannelOperationId=()=>Array.from(nativeRouteCrypto.random(24),byte=>byte.toString(16).padStart(2,'0')).join('');
/** One page owns every child source, including retained uncertain operations. */
export function createNativeChannels(context:NativeShellBootstrapContext,reportError:NativeShellRuntime['reportError']) {
  const lifetime=new NativeScreenLifetime(context),current=()=>lifetime.current()&&nativeStorageOwner(context),serverId=lifetime.authority.serverId;
  const api={request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T>=>current()?lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller):Promise.reject(screenScopeEnded())};
  const failure=(error:unknown)=>{const owned=current();dispose();if(owned)reportError(error);};
  const libraries=context.libraries().filter(library=>['movie','tv','anime'].includes(library.kind));
  const reader=new SettingsLibraryChannelsSource(api,serverId,current,failure),editor=new SettingsChannelEditorSource(api,serverId,reader,current,failure,nativeChannelOperationId),actions=new SettingsChannelActionsSource(api,serverId,reader,current,failure,nativeChannelOperationId),fields=new ChannelFieldsSource(api,libraries,current,failure),includeFields=new ChannelFieldsSource(api,libraries,current,failure),excludeFields=new ChannelFieldsSource(api,libraries,current,failure),presets=new BlockPresetsSource(api,serverId,current,failure,nativeChannelOperationId);
  const logos=new SettingsChannelLogoSource<NativeLogoImage>(nativeChannelLogoUpload(context,lifetime,current),serverId,reader,current,failure,nativeChannelOperationId);
  const logoCapability=()=>nativeChannelLogoCapability(context,lifetime,current);
  let selectedKey='[]';
  const offEditor=editor.subscribe(()=>{const snapshot=editor.getSnapshot(),ids=snapshot.phase==='editing'?snapshot.config?.rules.find(rule=>rule.id===snapshot.config?.defaultRuleId)?.query.libraryIds??[]:[],key=JSON.stringify(ids);if(key===selectedKey)return;selectedKey=key;void fields.select(ids);void includeFields.select(ids);void excludeFields.select(ids);});
  const dispose=()=>{offEditor();lifetime.dispose();fields.dispose();includeFields.dispose();excludeFields.dispose();presets.dispose();logos.dispose();editor.dispose();actions.dispose();reader.dispose();};
  let timezone:string|undefined;try{timezone=Intl.DateTimeFormat().resolvedOptions().timeZone;if(timezone)new Intl.DateTimeFormat(undefined,{timeZone:timezone});}catch{timezone=undefined;}
  return {context,api,reader,editor,actions,fields,includeFields,excludeFields,presets,logos,logoCapability,libraries,timezone,current,failure,dispose};
}
export type NativeChannelsGroup=ReturnType<typeof createNativeChannels>;
