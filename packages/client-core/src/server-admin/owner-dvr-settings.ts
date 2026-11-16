import {callServer} from '../query/server-api.ts';
import {putSettingsDocument, type SettingsDocumentAPI} from '../settings-document.ts';
import {DraftForm} from './draft-form.ts';
export type OwnerDvrSettings=Readonly<{folder:string;beforeMs:number;afterMs:number;floorBytes:number;capBytes:number;keepDays:number;keepNewest:number;stopViewers:boolean}>;
export type OwnerDvrDocument=Readonly<{value:OwnerDvrSettings;status:Readonly<{etag:string}>}>;
function loaded(raw:unknown):OwnerDvrDocument{
 const doc=raw as {values:OwnerDvrSettings;etag:string},value=doc?.values;
 if(!value||!/^"[0-9]+"$/.test(doc.etag)||typeof value.folder!=='string'||typeof value.stopViewers!=='boolean'||['beforeMs','afterMs','floorBytes','capBytes','keepDays','keepNewest'].some(key=>!Number.isSafeInteger(value[key as keyof OwnerDvrSettings])))throw new Error('invalid_recording_settings');
 return {value,status:{etag:doc.etag}};
}
export function readRecordingStorage(api:SettingsDocumentAPI,signal?:AbortSignal){return callServer(api,'GET /v1/dvr',{signal});}
export async function readOwnerDvrSettings(api:SettingsDocumentAPI,signal?:AbortSignal){return loaded(await callServer(api,'GET /v1/admin/settings/{name}',{id:'dvr',signal}));}
export async function saveOwnerDvrSettings(api:SettingsDocumentAPI,current:OwnerDvrDocument,change:Partial<OwnerDvrSettings>,signal?:AbortSignal){return loaded(await putSettingsDocument(api,'dvr',current.status.etag,change,signal));}
export function ownerRecordingDefaultsForm(api:SettingsDocumentAPI){return new DraftForm<OwnerDvrSettings,OwnerDvrDocument['status']>({
 load:signal=>readOwnerDvrSettings(api,signal),
 save:(draft,saved,status)=>{if(!status)throw new Error('invalid_recording_settings');return saveOwnerDvrSettings(api,{value:saved,status},Object.fromEntries(Object.entries(draft).filter(([key,value])=>value!==saved[key as keyof OwnerDvrSettings])));},
});}

/** The recording choices the Live TV pages offer, as exact values of the `dvr` document. A stored value that matches
 * no preset is shown as Custom, never rounded to one. */
export const DVR_PADDING_PRESETS={standard:{beforeMs:60000,afterMs:180000},exact:{beforeMs:0,afterMs:0},generous:{beforeMs:300000,afterMs:600000},sport:{beforeMs:60000,afterMs:900000}} as const;
export const DVR_KEEP_PRESETS={all:{keepDays:0,keepNewest:0},d30:{keepDays:30,keepNewest:0},d90:{keepDays:90,keepNewest:0},n5:{keepDays:0,keepNewest:5},n10:{keepDays:0,keepNewest:10}} as const;
export const DVR_SPACE_PRESETS={free10:{floorBytes:10*1024**3,capBytes:0},up250:{floorBytes:10*1024**3,capBytes:250*1024**3},up500:{floorBytes:10*1024**3,capBytes:500*1024**3},up1000:{floorBytes:10*1024**3,capBytes:1000*1024**3}} as const;
export function matchingDvrPreset(presets:Readonly<Record<string,Readonly<Record<string,number>>>>,value:object):string{
 return Object.keys(presets).find(key=>Object.entries(presets[key]!).every(([field,expected])=>(value as Record<string,unknown>)[field]===expected))??'custom';
}
/** Where recordings go and whether the server can write there; and each tuner's use. */
export function readDvrFolder(api:SettingsDocumentAPI,signal?:AbortSignal){return callServer(api,'GET /v1/admin/dvr/folder',{signal});}
export function readDvrTuners(api:SettingsDocumentAPI,signal?:AbortSignal){return callServer(api,'GET /v1/admin/dvr/tuners',{signal});}
