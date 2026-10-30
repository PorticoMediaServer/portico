import type {MessageId} from '../../i18n/src/index.ts';
import {routePaths, type RouteId} from '../../contracts/src/route-paths.ts';
import * as decoders from '../../contracts/src/decoders.ts';
import {userMutationRoute} from './user-mutation-routes.ts';
import {sameUserMutationScope, UserMutationError, type UserMutationCoordinator, type UserMutationEntry, type UserMutationResponse, type UserMutationScope} from './user-mutations.ts';

export type UserMutationRecoveryDestination = Readonly<{kind:'saved'} | {kind:'settings';section?:string;server:boolean} | {kind:'playback';sessionId?:string}>;
export type UserMutationRecoverySecretField = Readonly<{path:string;labelKey:MessageId;maxLength:number}>;
export type UserMutationRecoveryRow = Readonly<{key:string;category:'saved'|'settings'|'playback';status:'waiting'|'review'|'done';createdMs:number;canKeep:boolean;secretFields:readonly UserMutationRecoverySecretField[];destination:UserMutationRecoveryDestination}>;
export type UserMutationRecoverySnapshot = Readonly<{revision:number;rows:readonly UserMutationRecoveryRow[]}>;
export interface UserMutationRecoveryApi {
 awaitUserMutationsReady?():Promise<void>;
 request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;
 reenterUserMutationSecrets(key:string,body:unknown,signal?:AbortSignal):Promise<UserMutationResponse>;
 acknowledgeUserMutation(key:string):Promise<void>;
 dismissReviewedUserMutation(key:string):Promise<void>;
}
const savedOperations = new Set(['set_container_personal_state','undo_container_personal_state','put_items_personal_state_batch','put_items_id_personal_state','put_items_itemId_reading_favorite','put_items_itemId_reading_mark','put_items_itemId_reading_my_list','put_reading_group_my_list','post_libraries_libraryId_reading_mark_all','delete_reading_saved_group_reference','put_saved_pins_order','put_saved_pins_kind_id','post_saved_resources','delete_saved_resources_id','patch_saved_resources_id','put_saved_resources_id_entries','reorder_saved_collection','delete_saved_resources_id_shares','put_saved_resources_id_shares','post_playlists','delete_playlists_id','patch_playlists_id','post_playlists_id_entries','delete_playlists_id_entries_entryId','reorder_playlist_window','delete_playlists_id_shares','put_playlists_id_shares']);
const playbackOperations = new Set(['stop_playback_session','stop_account_playback']);
const category = (entry:UserMutationEntry):UserMutationRecoveryRow['category'] => playbackOperations.has(entry.operation)?'playback':savedOperations.has(entry.operation)?'saved':'settings';
function destination(entry:UserMutationEntry):UserMutationRecoveryDestination {
 const kind=category(entry);
 if(kind==='saved')return {kind};
 if(kind==='playback')return {kind}; // The existing player decides which current occurrence it owns.
 return {kind,server:!['patch_preferences','post_channels_preferences','post_library_channels_preferences','put_listening_preferences','put_download_settings'].includes(entry.operation),...(entry.operation==='put_metadata_settings'?{section:'metadata'}:entry.operation==='put_admin_backup_settings'?{section:'storage'}:entry.operation==='set_live_source_configuration'||entry.operation==='set_live_settings'?{section:'live'}:{})};
}
const field = (path:string,labelKey:MessageId,maxLength=2048):UserMutationRecoverySecretField => Object.freeze({path,labelKey,maxLength});
const secretAllowlist:Readonly<Record<string,readonly UserMutationRecoverySecretField[]>> = {
 put_admin_backup_settings:[field('passphrase','next.settings.backup.passphrase',1024)],
 put_metadata_settings:[field('tmdbReadToken','metadata.settings.tmdbToken'),field('tvdbProjectKey','metadata.settings.tvdbKey'),field('openSubtitlesApiKey','server.metadata.openSubtitlesKey'),field('openSubtitlesPassword','server.metadata.openSubtitlesPassword',512),field('acoustidKey','next.settingsSpec.server.metadata.advanced.groups.3.rows.0.label')],
 set_live_source_configuration:[field('settings.remote.password','next.remoteSources.password',2048),field('settings.remote.guideUrl','web.liveTv.guideUrl',8192)],
};
function missingFields(entry:UserMutationEntry):readonly UserMutationRecoverySecretField[] {
 if(entry.state!=='review'||entry.errorCode!=='secret_reentry_required'||!entry.requiresReentry||!entry.secretFields?.length)return Object.freeze([]);
 const allowed=secretAllowlist[entry.operation]??[],names=entry.secretFields;
 // Partial forms must never invite a retry that silently omits another required field.
 if(new Set(names).size!==names.length||names.some(path=>!allowed.some(field=>field.path===path)))return Object.freeze([]);
 return Object.freeze(names.map(path=>allowed.find(field=>field.path===path)!));
}
function releasable(entry:UserMutationEntry):boolean {
 if(entry.errorCode==='receipt_expired'||entry.errorCode==='receipt_unknown'||entry.state==='quarantined'||entry.state==='pending')return false;
 return entry.state==='confirmed'&&!!entry.receipt || entry.state==='review'&&!!(entry.terminalProof?.kind==='refused'||entry.receipt&&(['partial','conflict','invalid','terminal'].includes(entry.receipt.status)||entry.receipt.positionStatus==='policy_rejected'));
}
// This witness contains only existing safe metadata and is never persisted.
const witness=(entry:UserMutationEntry)=>JSON.stringify([entry.scope,entry.key,entry.state,entry.receipt,entry.terminalProof,entry.errorCode]);
const emptyRows:readonly UserMutationRecoveryRow[]=Object.freeze([]);

/** Presentation custody shared by mounted web/native shells. This owns no queue,
 * durable state, credentials or background retries. Review is a read, never proof
 * that unresolved history failed. Only explicit, guarded Keep as is consumes. */
export class UserMutationRecoveryModel {
 private active=true;
 private readonly originalScope:UserMutationScope|undefined;
 private readonly listeners=new Set<()=>void>();
 private readonly reviewed=new Map<string,string>();
 private readonly requests=new Set<AbortController>();
 private state:UserMutationRecoverySnapshot=Object.freeze({revision:0,rows:emptyRows});
 private readonly unsubscribe:()=>void;
 private readonly ready:Promise<void>;
 private readonly options:{api:UserMutationRecoveryApi;coordinator:UserMutationCoordinator;scope:()=>UserMutationScope|undefined};
 constructor(options:UserMutationRecoveryModel['options']) {
  this.options=options;
  const scope=options.scope();this.originalScope=scope?Object.freeze({...scope}):undefined;
  this.unsubscribe=options.coordinator.subscribe(this.refresh);this.refresh();
  this.ready=options.api.awaitUserMutationsReady?.()??Promise.resolve();
  void this.ready.then(()=>this.refresh(),()=>{/* The mounted transport retains and presents startup failures. */});
 }
 snapshot=():UserMutationRecoverySnapshot=>{if(!this.current()&&this.state.rows.length)this.refresh();return this.state;};
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private current(){const scope=this.options.scope();return this.active&&!!scope&&!!this.originalScope&&sameUserMutationScope(scope,this.originalScope);}
 private entries(){return this.current()?this.options.coordinator.snapshot().filter(entry=>sameUserMutationScope(entry.scope,this.originalScope!)):[];}
 private refresh=()=>{
  const entries=this.entries();
  for(const [key,proof] of this.reviewed)if(!entries.some(entry=>entry.key===key&&witness(entry)===proof))this.reviewed.delete(key);
  const rows=entries.map(entry=>Object.freeze({key:entry.key,category:category(entry),status:entry.state==='confirmed'?'done' as const:entry.state==='pending'?'waiting' as const:'review' as const,createdMs:Number(entry.key.split('_')[1]),canKeep:releasable(entry)&&this.reviewed.get(entry.key)===witness(entry),secretFields:missingFields(entry),destination:Object.freeze(destination(entry))}));
  if(JSON.stringify(rows)===JSON.stringify(this.state.rows))return;
  this.state=Object.freeze({revision:this.state.revision+1,rows:Object.freeze(rows)});
  for(const listener of this.listeners)listener();
 };
 private entry(key:string){const entry=this.entries().find(entry=>entry.key===key);if(!entry)throw new UserMutationError('mutation_scope_changed',key);return entry;}
 async checkAgain(key:string):Promise<void>{await this.ready;this.entry(key);await this.options.coordinator.reconcile(key);this.refresh();}
 async review(key:string,signal?:AbortSignal):Promise<UserMutationRecoveryDestination>{
  await this.ready;
  const entry=this.entry(key),proof=witness(entry),controller=new AbortController(),abort=()=>controller.abort();
  if(signal?.aborted)controller.abort();signal?.addEventListener('abort',abort,{once:true});this.requests.add(controller);
  try {
   const read=currentRead(entry),raw=await this.options.api.request<unknown>(read.path,'GET',undefined,controller.signal);
   const parsed=read.parse(raw);
   if(parsed&&typeof parsed==='object'&&'scope' in parsed){const scope=(parsed as {scope?:{serverId?:unknown}}).scope;if(scope?.serverId!==this.originalScope?.serverId)throw new UserMutationError('mutation_scope_changed',key);}
   if(controller.signal.aborted||witness(this.entry(key))!==proof)throw new UserMutationError('mutation_scope_changed',key);
   this.reviewed.set(key,proof);this.refresh();return Object.freeze(destination(entry));
  } finally {this.requests.delete(controller);signal?.removeEventListener('abort',abort);}
 }
 async keepAsIs(key:string):Promise<void>{
  await this.ready;
  const entry=this.entry(key);
  if(!releasable(entry)||this.reviewed.get(key)!==witness(entry))throw new UserMutationError('mutation_needs_review',key);
  // API acknowledgement also honours the native Stop owner/consumer handshake.
  if(entry.state==='confirmed')await this.options.api.acknowledgeUserMutation(key);
  else await this.options.api.dismissReviewedUserMutation(key);
  this.reviewed.delete(key);this.refresh();
 }
 async reenter(key:string,values:Readonly<Record<string,string>>,signal?:AbortSignal):Promise<void>{
  await this.ready;
  const entry=this.entry(key),fields=missingFields(entry);
  if(!fields.length||Object.keys(values).length!==fields.length||fields.some(field=>typeof values[field.path]!=='string'||!values[field.path]||values[field.path]!.length>field.maxLength))throw new UserMutationError('mutation_needs_review',key);
  // The wire body substitutes the protocol key for caller correlation IDs.
  // Re-entry must rebuild the authored safe body used by the fingerprint;
  // transport substitutes the same original wire key again only when sending.
  const authored=JSON.parse(entry.fingerprint) as {body?:unknown};
  const body:unknown=authored.body;
  if(!body||typeof body!=='object'||Array.isArray(body))throw new UserMutationError('mutation_needs_review',key);
  for(const field of fields){const parts=field.path.split('.');let parent=body as Record<string,unknown>;for(const part of parts.slice(0,-1)){const child=parent[part];if(!child||typeof child!=='object'||Array.isArray(child))throw new UserMutationError('mutation_needs_review',key);parent=child as Record<string,unknown>;}parent[parts[parts.length-1]!]=values[field.path];}
  // Never store the merged secret body, nor a fingerprint of it, in this model.
  this.entry(key);await this.options.api.reenterUserMutationSecrets(key,body,signal);this.refresh();
 }
 dispose=()=>{if(!this.active)return;this.active=false;this.unsubscribe();for(const request of this.requests)request.abort();this.requests.clear();this.reviewed.clear();this.state=Object.freeze({revision:this.state.revision+1,rows:emptyRows});for(const listener of this.listeners)listener();this.listeners.clear();};
}

type CurrentRead={path:string;parse:(raw:unknown)=>unknown};
const settingsReaders:Readonly<Record<string,readonly [RouteId,(raw:unknown)=>unknown]>> = {
 put_admin_backup_settings:['get_admin_backup_settings',decoders.decodeGetAdminBackupSettingsResponse],
 change_console_settings:['get_console_settings',decoders.decodeGetConsoleSettingsResponse],
 put_admin_dvr_settings:['get_admin_dvr_settings',decoders.decodeGetAdminDvrSettingsResponse],
 patch_admin_libraries_id:['get_admin_libraries_id',decoders.decodeGetAdminLibrariesIdResponse],
 put_library_settings:['get_library_settings',decoders.decodeGetLibrarySettingsResponse],
 put_admin_libraries_id_sonic_analysis:['get_admin_libraries_id_sonic_analysis',decoders.decodeGetAdminLibrariesIdSonicAnalysisResponse],
 set_live_source_configuration:['get_live_source_configuration',decoders.decodeGetLiveSourceConfigurationResponse],
 set_live_settings:['get_live_settings',decoders.decodeGetLiveSettingsResponse],
 patch_admin_logs_settings:['get_admin_logs_settings',decoders.decodeGetAdminLogsSettingsResponse],
 put_admin_maintenance_settings:['get_admin_maintenance_settings',decoders.decodeGetAdminMaintenanceSettingsResponse],
 put_metadata_settings:['get_metadata_settings',decoders.decodeGetMetadataSettingsResponse],
 patch_admin_notifications_settings:['get_admin_notifications_settings',decoders.decodeGetAdminNotificationsSettingsResponse],
 put_download_settings:['get_download_settings',decoders.decodeGetDownloadSettingsResponse],
 put_items_itemId_reading_favorite:['get_items_itemId_reading_favorite',decoders.decodeGetItemsItemIdReadingFavoriteResponse],
 put_items_itemId_reading_mark:['get_items_itemId_reading_mark',decoders.decodeGetItemsItemIdReadingMarkResponse],
 put_items_itemId_reading_my_list:['get_items_itemId_reading_my_list',decoders.decodeGetItemsItemIdReadingMyListResponse],
 put_libraries_id_metadata_agent:['get_libraries_id_metadata_agent',decoders.decodeGetLibrariesIdMetadataAgentResponse],
 put_libraries_id_metadata_screen:['get_libraries_id_metadata_screen',decoders.decodeGetLibrariesIdMetadataScreenResponse],
 put_libraries_id_strm_analysis_policy:['get_libraries_id_strm_analysis_policy',decoders.decodeGetLibrariesIdStrmAnalysisPolicyResponse],
 put_reading_group_my_list:['get_reading_group_my_list',decoders.decodeGetReadingGroupMyListResponse],
 put_listening_preferences:['get_listening_preferences',decoders.decodeGetListeningPreferencesResponse],
 post_networking_remote_config:['get_networking_remote',decoders.decodeGetNetworkingRemoteResponse],
 patch_preferences:['get_preferences',decoders.decodeGetPreferencesResponse],
 put_storage_source_advanced:['get_storage_source_advanced',decoders.decodeGetStorageSourceAdvancedResponse],
 put_storage_source_read_profile:['get_storage_source_read_profile',decoders.decodeGetStorageSourceReadProfileResponse],
 put_admin_libraries_order:['get_admin_libraries',decoders.decodeGetAdminLibrariesResponse],
 put_admin_libraries_id_scan_policy:['get_library_settings',decoders.decodeGetLibrarySettingsResponse],
 put_admin_libraries_id_shows_show_settings:['get_library_settings',decoders.decodeGetLibrarySettingsResponse],
 put_storage_source_program:['get_storage_sources',decoders.decodeGetStorageSourcesResponse],
 post_channels_preferences:['get_guide_channels',decoders.decodeGetGuideChannelsResponse],
 post_library_channels_preferences:['get_guide_channels',decoders.decodeGetGuideChannelsResponse],
};
function currentRead(entry:UserMutationEntry):CurrentRead {
 const kind=category(entry);
 if(kind==='playback')return {path:routePaths.get_remote_playback_controls,parse:decoders.decodeGetRemotePlaybackControlsResponse};
 if(kind==='saved'){
  if(entry.operation.includes('saved_pins'))return {path:routePaths.get_saved_pins_order,parse:decoders.decodeGetSavedPinsOrderResponse};
  if(entry.operation.includes('saved_resources')||entry.operation==='reorder_saved_collection')return {path:routePaths.get_saved_resources+'?limit=40',parse:decoders.decodeGetSavedResourcesResponse};
  return {path:routePaths.get_saved_content+'?view='+(entry.operation.includes('playlist')?'playlists':entry.operation.includes('favorite')?'favorites':'watchlist')+'&limit=40',parse:decoders.decodeGetSavedContentResponse};
 }
 const reader=settingsReaders[entry.operation],writer=userMutationRoute(entry.path,entry.method);
 if(!reader||!writer||writer.id!==entry.operation)throw new UserMutationError('mutation_readback_unavailable',entry.key);
 const segments=entry.path.split('?')[0]!.split('/'),params=Object.fromEntries(writer.path.split('/').flatMap((part,index)=>part.startsWith('{')?[[part.slice(1,-1),segments[index]]]:[]));
 let path:string=routePaths[reader[0]].replace(/\{([^}]+)\}/g,(_,name:string)=>{if(!params[name])throw new UserMutationError('mutation_readback_unavailable',entry.key);return params[name];});
 if(reader[0]==='get_guide_channels')path+='?kind='+(entry.operation==='post_channels_preferences'?'live-source':'library-channel')+'&timezone=UTC&includeHidden=true&limit=40';
 if(reader[0]==='get_preferences'){
  const device=entry.recoveryDeviceClass;
  if(!device||!['web','mobile','tablet','television'].includes(device))throw new UserMutationError('mutation_readback_unavailable',entry.key);
  path+='?deviceClass='+encodeURIComponent(device);
 }
 return {path,parse:reader[1]};
}
