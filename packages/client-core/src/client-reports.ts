import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {parseEnvelope} from './administration.ts';
import {keepReadable} from './tolerant.ts';

export type ClientReport=Readonly<{id:string;accountId:string;profileId:string;deviceId:string;platform:string;deviceClass:string;appVersion:string;receivedAt:string;bytes:number;body?:string}>;
export type ClientReportInput=Readonly<{platform:string;deviceClass:string;appVersion:string;body:string}>;
export type ReportTransport={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
export type ClientReportPage=Readonly<{items:readonly ClientReport[];limit:number;nextCursor?:string}>;
function report(value:unknown):ClientReport{
 if(!value||typeof value!=='object')throw new Error('Invalid client report.');
 const v=value as Record<string,unknown>;
 for(const key of ['id','accountId','profileId','deviceId','platform','deviceClass','appVersion','receivedAt']){
  if(typeof v[key]!=='string'||(v[key] as string).length>160)throw new Error('Invalid client report.');
 }
 if(typeof v.bytes!=='number'||!Number.isSafeInteger(v.bytes)||v.bytes<0||v.bytes>262144)throw new Error('Invalid client report.');
 if(v.body!==undefined&&(typeof v.body!=='string'||v.body.length>262144))throw new Error('Invalid client report.');
 return Object.freeze(v) as ClientReport;
}
export async function sendClientReport(transport:ReportTransport,serverId:string,input:ClientReportInput,signal?:AbortSignal):Promise<ClientReport>{
 return parseEnvelope(await transport.request<RouteResponse<"post_client_log">>(routePath("post_client_log"),'POST',input,signal),serverId,report).result;
}
export async function listClientReports(transport:ReportTransport,serverId:string,cursor='',signal?:AbortSignal):Promise<ClientReportPage>{
 const raw=await transport.request<RouteResponse<"get_client_logs">>(routePath("get_client_logs") + "?limit=25" + (cursor?`&cursor=${encodeURIComponent(cursor)}`:''),'GET',undefined,signal);
 return parseEnvelope(raw,serverId,value=>{
  if(!value||typeof value!=='object')throw new Error('Invalid client reports.');
  const v=value as Record<string,unknown>;
  if(!Array.isArray(v.items)||v.items.length>100||typeof v.limit!=='number'||v.limit<1||v.limit>100||v.nextCursor!==undefined&&typeof v.nextCursor!=='string')throw new Error('Invalid client reports.');
  // One report that cannot be read is left out alone; the rest are listed.
  return Object.freeze({items:Object.freeze(keepReadable('client-reports','items',v.items,report)),limit:v.limit,...(v.nextCursor?{nextCursor:v.nextCursor as string}:{})});
 }).result;
}
export async function readClientReport(transport:ReportTransport,serverId:string,id:string,signal?:AbortSignal):Promise<ClientReport>{
 return parseEnvelope(await transport.request<RouteResponse<"get_client_log">>(encodedRoutePath("get_client_log", {"id": encodeURIComponent(id)}),'GET',undefined,signal),serverId,report).result;
}
export async function deleteClientReport(transport:ReportTransport,id:string,signal?:AbortSignal):Promise<void>{
 await transport.request(encodedRoutePath("delete_client_log", {"id": encodeURIComponent(id)}),'DELETE',undefined,signal);
}
export function clientReportDownloadPath(id:string):string{return encodedRoutePath("download_client_log", {"id": encodeURIComponent(id)});}
