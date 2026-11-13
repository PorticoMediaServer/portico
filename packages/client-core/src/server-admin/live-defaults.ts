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
