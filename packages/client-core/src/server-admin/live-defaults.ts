import {callServer} from '../query/server-api.ts';
import {putSettingsDocument, type SettingsDocumentAPI} from '../settings-document.ts';
import {DraftForm} from './draft-form.ts';
export type OwnerLiveDefaults=Readonly<{bufferSeconds:number;retrySeconds:number;guideDays:number;rewindMinutes:number;importImages:boolean;discoveryEnabled:boolean;userAgent:string}>;
export type LiveDefaultsStatus=Readonly<{etag:string}>;
function loaded(raw:unknown){
 const doc=raw as {values:OwnerLiveDefaults;etag:string},values=doc?.values;
 if(!values||!/^"[0-9]+"$/.test(doc.etag)||['bufferSeconds','retrySeconds','guideDays','rewindMinutes'].some(key=>!Number.isSafeInteger(values[key as keyof OwnerLiveDefaults]))||typeof values.importImages!=='boolean'||typeof values.discoveryEnabled!=='boolean'||typeof values.userAgent!=='string')throw new Error('invalid_live_settings');
 return {value:values,status:{etag:doc.etag}};
}
/** The rebuilt live document, in its actual units, with the edited document's ETag. */
export function ownerLiveDefaultsForm(api:SettingsDocumentAPI){return new DraftForm<OwnerLiveDefaults,LiveDefaultsStatus>({
 load:async signal=>loaded(await callServer(api,'GET /v1/admin/settings/{name}',{id:'live',signal})),
 save:async(draft,saved,status)=>{if(!status)throw new Error('invalid_live_settings');const change=Object.fromEntries(Object.entries(draft).filter(([name,value])=>value!==saved[name as keyof OwnerLiveDefaults]));return loaded(await putSettingsDocument(api,'live',status.etag,change));},
});}
export type LiveDefaultsDocument=Readonly<{value:OwnerLiveDefaults;status:LiveDefaultsStatus}>;
/** The live document read once, for a screen that saves it by itself (the phone's Live defaults sheet). */
export async function readLiveDefaultsDocument(api:SettingsDocumentAPI,signal?:AbortSignal):Promise<LiveDefaultsDocument>{return loaded(await callServer(api,'GET /v1/admin/settings/{name}',{id:'live',signal}));}
/** Saves only the fields that changed, at the revision read. */
export async function saveLiveDefaultsDocument(api:SettingsDocumentAPI,current:LiveDefaultsDocument,next:OwnerLiveDefaults,signal?:AbortSignal):Promise<LiveDefaultsDocument>{
 const change=Object.fromEntries(Object.entries(next).filter(([name,value])=>value!==current.value[name as keyof OwnerLiveDefaults]));
 return loaded(await putSettingsDocument(api,'live',current.status.etag,change,signal));
}
