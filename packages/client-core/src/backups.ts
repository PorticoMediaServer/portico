import {throwIfAborted} from './abort-lifetime.ts';
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {parseBackupsDocument,parseEnvelope,type BackupsDocument,type RestoreRecovery,type RestoreSource,type RestoreStaged} from './administration.ts';
import type {I18n} from '../../i18n/src/index.ts';
import {sentence} from './server-admin/console-words.ts';

/** The owner notice for a damaged backup or database, based only on the server's recovery report. */
export function restoreRecoveryNotice(recovery?:RestoreRecovery):'repaired'|'recovered'|'partial'|undefined{
 if(recovery?.manifestDamage&& (recovery.manifestDamage.count>0||recovery.manifestDamage.missingFiles>0||recovery.manifestDamage.unverifiedDatabase))return 'partial';
 if(recovery?.salvaged)return (recovery.damagedTables?.length??0)>0||(recovery.removedRows??0)>0?'partial':'recovered';
 return recovery?.indexesRebuilt?'repaired':undefined;
}

/** A bounded owner report. Counts and offsets stay exact, including an empty EOF region. */
export function restoreRecoveryDetails(recovery:RestoreRecovery|undefined,t:I18n['t']):readonly string[]{
 const damage=recovery?.manifestDamage;
 if(!damage)return Object.freeze([]);
 const details=[t('web.maintenance.restoreManifestDamage',{count:String(damage.count),missingFiles:String(damage.missingFiles)})];
 for(const region of damage.regions)details.push(t('web.maintenance.restoreManifestRegion',{
  firstLine:String(region.firstLine),lastLine:String(region.lastLine),startByte:String(region.startByte),endByte:String(region.endByte),reason:sentence(region.reason),
 }));
 if(damage.regionsTruncated)details.push(t('web.maintenance.restoreManifestTruncated',{shown:String(damage.regions.length),count:String(damage.count)}));
 if(damage.unverifiedDatabase)details.push(t('web.maintenance.restoreDatabaseUnverified'));
 return Object.freeze(details);
}

/** Typed clients for the spec §4 backup routes (`Spec — Backups (Plex model).md`).
 * Parsers live in administration.ts; this module holds transport only, no stored credentials.
 * TODO(be backups): switch to generated types once apigen publishes these routes. */
export type BackupTransport={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};

const BACKUPS_PATH=routePath("get_admin_backups");
const RESTORE_PATH=routePath("post_admin_backups_restore");
/** The owner-only fix-permissions operation (spec §6.2), as be/backups publishes it. */
export const STATE_PERMISSIONS_FIX_PATH=routePath("post_admin_state_permissions_fix");

/** getBackups reads the whole backups document: the list, the running backup (if any) and the last restore. */
export async function getBackups(transport:BackupTransport,serverId:string,signal?:AbortSignal):Promise<BackupsDocument>{
 return parseEnvelope(await transport.request<unknown>(BACKUPS_PATH,'GET',undefined,signal),serverId,parseBackupsDocument).result;
}

/** startBackup starts one backup. `operationId` is the caller's: one per logical submission,
 * reused on retry (M19). Progress appears in `running`, read back with getBackups. */
export async function startBackup(transport:BackupTransport,serverId:string,operationId:string,signal?:AbortSignal):Promise<string>{
 return parseEnvelope(await transport.request<unknown>(BACKUPS_PATH,'POST',{operationId},signal),serverId,v=>{
  if(typeof v!=='object'||v===null||typeof (v as {jobId?:unknown}).jobId!=='string'||!(v as {jobId:string}).jobId)throw new Error('Invalid backup receipt.');
  return (v as {jobId:string}).jobId;
 }).result;
}

/** deleteBackup removes one listed backup. */
export async function deleteBackup(transport:BackupTransport,serverId:string,backupId:string,signal?:AbortSignal):Promise<void>{
 await transport.request<unknown>(`${BACKUPS_PATH}/${encodeURIComponent(backupId)}`,'DELETE',undefined,signal);
}

/** restoreBackup stages a backup (by list id) or a server path (a backup folder or a `.db` file)
 * and answers `{staged, restartRequired}`. A 4xx refusal carries one of the spec §4 codes
 * (`isRestoreRefusalCode`), which the console maps to catalogue copy. */
export type RestoreObserver = Readonly<{isCurrent?: () => boolean; onAccepted?: (jobId: string) => void; onProgress?: (document: BackupsDocument) => void}>;
export async function restoreBackup(transport:BackupTransport,serverId:string,operationId:string,source:RestoreSource,signal?:AbortSignal,passphrase?:string,observer:RestoreObserver={}):Promise<RestoreStaged>{
 const requireCurrent=()=>{throwIfAborted(signal);if(observer.isCurrent&&!observer.isCurrent())throw new DOMException('The restore scope ended.','AbortError');};
 requireCurrent();
 const receipt=parseEnvelope(await transport.request<unknown>(RESTORE_PATH,'POST',{operationId,source,...(passphrase!==undefined?{passphrase}:{})},signal),serverId,v=>{
  if(typeof v!=='object'||v===null||typeof (v as {jobId?:unknown}).jobId!=='string'||!(v as {jobId:string}).jobId||(v as {jobId:string}).jobId.length>128)throw new Error('Invalid restore receipt.');
  return (v as {jobId:string}).jobId;
 }).result;
 requireCurrent();observer.onAccepted?.(receipt);requireCurrent();
 const started=Date.now();
 for(;;){
  requireCurrent();
  let doc:BackupsDocument;
  try{doc=await getBackups(transport,serverId,signal);}catch(e){
   const status=(e as {status?:number})?.status;
   const code=(e as {code?:string})?.code;
   if(authenticationFailure(e)||code==='wrong_server'||code==='invalid_administration_response'||status!==undefined&&status<500&&status!==429)throw e;
   if(Date.now()-started>600_000)throw Object.assign(new Error('The restore is still running.'),{code:'restore_progress_timeout'});
   await delay(500,signal);continue;
  }
  requireCurrent();
  if(doc.lastRestore?.jobId===receipt||doc.restore?.jobId===receipt){observer.onProgress?.(doc);requireCurrent();}
  if(doc.lastRestore?.jobId===receipt){
   if(doc.lastRestore.outcome==='rolled_back')throw Object.assign(new Error('The restore was rolled back.'),{code:'restore_integrity_failed'});
   const recovery=doc.lastRestore.recovery;
   return {staged:true,restartRequired:true,validation:{schemaVersion:doc.lastRestore.schemaVersion??0,integrity:restoreRecoveryNotice(recovery)?'recovered':'ok',...(recovery?{recovery}:{})}};
  }
  if(doc.restore?.jobId===receipt){
   if(doc.restore.errorCode)throw Object.assign(new Error('The restore could not be staged.'),{code:doc.restore.errorCode});
   if(doc.restore.result)return doc.restore.result;
  }
  if(Date.now()-started>600_000)throw Object.assign(new Error('The restore is still running.'),{code:'restore_progress_timeout'});
  await delay(500,signal);
 }
}

/** fixStatePermissions runs the owner-only §6.2 permissions repair. See STATE_PERMISSIONS_FIX_PATH. */
export async function fixStatePermissions(transport:BackupTransport,serverId:string,operationId:string,signal?:AbortSignal):Promise<void>{
 await transport.request<unknown>(STATE_PERMISSIONS_FIX_PATH,'POST',{operationId},signal);
}

export type BackupPollOptions=Readonly<{expectedJobId?:string;intervalMs?:number;timeoutMs?:number;signal?:AbortSignal;onProgress?:(doc:BackupsDocument)=>void}>;

function delay(ms:number,signal?:AbortSignal):Promise<void>{
 return new Promise<void>((resolve,reject)=>{
  if(signal?.aborted){reject(Object.assign(new Error('The wait was cancelled.'),{code:'cancelled'}));return;}
  if(ms<=0){resolve();return;}
  const onAbort=()=>{clearTimeout(timer);reject(Object.assign(new Error('The wait was cancelled.'),{code:'cancelled'}));};
  const timer=setTimeout(()=>{signal?.removeEventListener('abort',onAbort);resolve();},ms);
  signal?.addEventListener('abort',onAbort,{once:true});
 });
}

function authenticationFailure(e:unknown):boolean{
 const status=(e as {status?:unknown}|null)?.status;
 if(status===401||status===403)return true;
 const code=(e as {code?:unknown}|null)?.code;
 return code==='unauthorized'||code==='forbidden'||code==='authentication_required'||code==='session_expired'||code==='invalid_token';
}

/** isAuthenticationFailure answers whether a failure ended the sign-in (the app opens
 * sign-in again) rather than reporting a task failure. */
export function isAuthenticationFailure(e:unknown):boolean{
 return authenticationFailure(e);
}

/** waitForBackupCompletion polls getBackups every 2 s until `running` clears, reporting each
 * read through `onProgress`. Pass the start receipt as expectedJobId so a fast
 * completion can be matched even before the first running observation. Idle
 * state alone never proves completion. Abort stops polling. */
export async function waitForBackupCompletion(get:()=>Promise<BackupsDocument>,options:BackupPollOptions={}):Promise<BackupsDocument>{
 const intervalMs=options.intervalMs??2000,timeoutMs=options.timeoutMs??600_000;
 let expected=options.expectedJobId;
 const started=Date.now();
 for(;;){
  throwIfAborted(options.signal);
  const doc=await get();
  options.onProgress?.(doc);
  expected??=doc.running?.jobId;
  if(doc.lastBackup&&expected===doc.lastBackup.jobId){
   if(doc.lastBackup.outcome==='failed')throw Object.assign(new Error('The backup could not be completed.'),{code:doc.lastBackup.errorCode??'internal'});
   return doc;
  }
  if(Date.now()-started>timeoutMs)throw Object.assign(new Error('The backup is still running.'),{code:'backup_progress_timeout'});
  await delay(intervalMs,options.signal);
 }
}

/** waitForServerAnswer polls until the server answers again after the restore restart, then
 * returns the document the result is read from (`lastRestore`). Connection failures are
 * "not yet"; an authentication failure propagates, so an ended sign-in still lands on the
 * sign-in screen instead of sitting on "Restarting…". */
export async function waitForServerAnswer(get:()=>Promise<BackupsDocument>,options:BackupPollOptions={}):Promise<BackupsDocument>{
 const intervalMs=options.intervalMs??2000,timeoutMs=options.timeoutMs??180_000;
 const started=Date.now();
 for(;;){
  throwIfAborted(options.signal);
  try{
   return await get();
  }catch(e){
   if(authenticationFailure(e)||(e as {name?:unknown})?.name==='AbortError'||options.signal?.aborted)throw e;
   if(Date.now()-started>timeoutMs)throw Object.assign(new Error('The server has not answered yet.'),{code:'server_restart_timeout'});
   await delay(intervalMs,options.signal);
  }
 }
}

/** Restart is owner-only and works for portable launches as well as services. */
export async function restartServer(transport:BackupTransport,signal?:AbortSignal):Promise<void>{
 await transport.request<RouteResponse<"post_admin_restart">>(routePath("post_admin_restart"),'POST',routeBody("post_admin_restart", {}),signal);
}

export type BackupSettings=Readonly<{backupFolder:string;includeArtwork:boolean;passphraseProtected:boolean;passphraseSet:boolean}>;
export type BackupSettingsPatch=Readonly<{backupFolder?:string;includeArtwork?:boolean;passphraseProtected?:boolean;passphrase?:string}>;
function parseBackupSettings(v:unknown):BackupSettings{
 if(typeof v!=='object'||v===null)throw new Error('Invalid backup settings.');
 const s=v as Record<string,unknown>;
 if(typeof s.backupFolder!=='string'||s.backupFolder.length>8192||typeof s.includeArtwork!=='boolean'||typeof s.passphraseProtected!=='boolean'||typeof s.passphraseSet!=='boolean')throw new Error('Invalid backup settings.');
 return Object.freeze({backupFolder:s.backupFolder,includeArtwork:s.includeArtwork,passphraseProtected:s.passphraseProtected,passphraseSet:s.passphraseSet});
}
/** Secret write input is never retained or returned by the settings read facade. */
export async function getBackupSettings(transport:BackupTransport,serverId:string,signal?:AbortSignal):Promise<BackupSettings>{
 return parseEnvelope(await transport.request<unknown>(routePath('get_admin_backup_settings'),'GET',undefined,signal),serverId,parseBackupSettings).result;
}
export async function updateBackupSettings(transport:BackupTransport,serverId:string,patch:BackupSettingsPatch,signal?:AbortSignal):Promise<BackupSettings>{
 return parseEnvelope(await transport.request<unknown>(routePath('put_admin_backup_settings'),'PUT',routeBody('put_admin_backup_settings',patch),signal),serverId,parseBackupSettings).result;
}

export type BackupSourceInspection=Readonly<{encrypted:boolean}>;
/** Protection comes from captured bytes, never the source path's suffix. */
export async function inspectBackupSource(transport:BackupTransport,serverId:string,source:RestoreSource,signal?:AbortSignal):Promise<BackupSourceInspection>{
 return parseEnvelope(await transport.request<unknown>(routePath('post_admin_backups_inspect'),'POST',routeBody('post_admin_backups_inspect',{source}),signal),serverId,v=>{
  if(typeof v!=='object'||v===null||typeof (v as {encrypted?:unknown}).encrypted!=='boolean')throw new Error('Invalid backup inspection.');
  return Object.freeze({encrypted:(v as {encrypted:boolean}).encrypted});
 }).result;
}
