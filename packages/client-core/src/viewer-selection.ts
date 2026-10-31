import {parseLocalSession} from './local-session.ts';
import type {LocalSession} from './index.ts';

/** This record is an expiring server credential, never an account credential. */
export type SelectedViewerRecord=Readonly<{version:1;serverUrl:string;session:LocalSession}>;
export interface SelectedViewerStorage {
 read():Promise<SelectedViewerRecord|null>;write(record:SelectedViewerRecord|null):Promise<void>;
 /** Removes the saved record only when it is the given sign-in's, as one step where the store can. Absent: read, compare, then write. */
 clearIfFamily?(sessionFamilyId:string):Promise<void>;
}
export function selectedViewerRecord(serverUrl:string,raw:unknown,stored=false):SelectedViewerRecord {
 const url=new URL(serverUrl);if(!['https:','http:'].includes(url.protocol)||url.username||url.password||url.search||url.hash)throw new Error('A server origin without credentials is required.');
 // The refresh credential stays in the protected connection store only (see session-refresh.ts).
 const {refreshToken:_refresh,...session}=parseLocalSession(raw,{},{stored});
 return Object.freeze({version:1,serverUrl:url.href.replace(/\/+$/,''),session:Object.freeze(session)});
}
export function parseSelectedViewerRecord(raw:unknown):SelectedViewerRecord|null {
 if(raw===null)return null;
 if(!raw||typeof raw!=='object'||Array.isArray(raw)||!('version'in raw)||raw.version!==1||!('serverUrl'in raw)||typeof raw.serverUrl!=='string'||!('session'in raw))throw new Error('The saved server selection is invalid.');
 // A saved selection may have expired since; the server decides that when it is verified.
 return selectedViewerRecord(raw.serverUrl,raw.session,true);
}
