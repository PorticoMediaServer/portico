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
export async function readOwnerDvrSettings(api:SettingsDocumentAPI,signal?:AbortSignal){return loaded(await callServer(api,'GET /v1/admin/settings/{name}',{id:'dvr',signal}));}
export async function saveOwnerDvrSettings(api:SettingsDocumentAPI,current:OwnerDvrDocument,change:Partial<OwnerDvrSettings>,signal?:AbortSignal){return loaded(await putSettingsDocument(api,'dvr',current.status.etag,change,signal));}
export function ownerRecordingDefaultsForm(api:SettingsDocumentAPI){return new DraftForm<OwnerDvrSettings,OwnerDvrDocument['status']>({
 load:signal=>readOwnerDvrSettings(api,signal),
 save:(draft,saved,status)=>{if(!status)throw new Error('invalid_recording_settings');return saveOwnerDvrSettings(api,{value:saved,status},Object.fromEntries(Object.entries(draft).filter(([key,value])=>value!==saved[key as keyof OwnerDvrSettings])));},
});}
