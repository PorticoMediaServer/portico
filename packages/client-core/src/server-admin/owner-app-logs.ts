import type {HttpLocalApi} from '../index.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import type {ProblemReport, ServerSkippedFile, LogLine, ActivityEntry} from '../../../contracts/src/next/api.ts';
export type AppReport=ProblemReport;
export type RecordLane='runtime'|'audit';
export type OwnerRecord=Readonly<{at:string;level?:string;kind?:string;message:string;attrs?:Record<string,string>;actor?:string;target?:string;detail?:string}>;
export type RecordsSlice=Readonly<{lane:RecordLane;items:readonly OwnerRecord[];next?:string}>;
export type ReportsSnapshot=Readonly<{phase:'idle'|'loading'|'ready'|'error';reports:readonly AppReport[];next?:string;setAside?:Readonly<{items:readonly ServerSkippedFile[];next?:string}>;records?:RecordsSlice}>;
export type OwnerReportsTransport=ServerRequester & Partial<Pick<HttpLocalApi,'readReportLog'>>;
const empty:ReportsSnapshot=Object.freeze({phase:'idle',reports:[]});
const runtime=(line:LogLine):OwnerRecord=>({at:line.time,level:line.level,kind:line.kind,message:line.message,attrs:line.attrs});
const audit=(line:ActivityEntry):OwnerRecord=>({at:line.at,actor:line.actor,message:line.action,target:line.target,detail:line.detail});
/** Owner App logs and the real skipped-file list. No legacy catalogue failure or diagnostics receipt. */
export class OwnerAppLogsSource {
 private state:ReportsSnapshot=empty;private lifetime=new AbortController();private listeners=new Set<()=>void>();private reading?:Promise<void>;private recordsGeneration=0;private removing?:object;
 private transport:ServerRequester;
 constructor(private api:OwnerReportsTransport,_serverId:string,_viewerId:string,private isCurrent:()=>boolean,private reportError:(error:unknown)=>void=()=>{},private sections:Readonly<{reports:boolean;stuckFiles:boolean}>={reports:true,stuckFiles:true}){
  this.transport={request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>this.request(signal,current=>api.request<T>(path,method,body,current))};
 }
 private current(){if(this.lifetime.signal.aborted)return false;if(!this.isCurrent()){this.dispose();return false;}return true;}
 private async request<T>(input:AbortSignal|undefined,send:(signal:AbortSignal)=>Promise<T>):Promise<T>{
  if(!this.current()||input?.aborted)throw new DOMException('Report scope ended','AbortError');const signal=input?AbortSignal.any([input,this.lifetime.signal]):this.lifetime.signal;
  try{const result=await send(signal);if(!this.current()||signal.aborted)throw new DOMException('Report scope ended','AbortError');return result;}
  catch(error){const e=error as {status?:number;statusCode?:number;code?:string};if(this.current()&&([e?.status,e?.statusCode].some(status=>status===401||status===403)||['unauthorized','forbidden','not_permitted'].includes(e?.code??''))){this.dispose();this.reportError(Object.assign(new Error('reports_authority_ended'),{status:e.status===401||e.statusCode===401?401:403}));}throw error;}
 }
 getSnapshot=()=>this.current()?this.state:empty;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private publish(state:ReportsSnapshot){if(!this.current())return;this.state=Object.freeze(state);for(const listener of [...this.listeners]){if(!this.current())break;listener();}}
 listed=(report:AppReport)=>this.current()&&this.state.reports.includes(report);
 private reports(cursor='',signal?:AbortSignal){return callServer(this.transport,'GET /v1/admin/reports',{query:{kind:'crash',cursor},signal});}
 private async records(lane:RecordLane,cursor=''){if(lane==='runtime'){const page=await callServer(this.transport,'GET /v1/admin/logs',{query:{cursor}});return {items:page.items.map(runtime),next:page.nextCursor};}const page=await callServer(this.transport,'GET /v1/admin/audit',{query:{cursor}});return {items:page.items.map(audit),next:page.nextCursor};}
 load=():Promise<void>=>{
  if(!this.current()||this.reading)return this.reading??Promise.resolve();const generation=++this.recordsGeneration;
  const task=Promise.resolve().then(async()=>{if(!this.current())return;this.publish({...this.state,phase:'loading'});
   try{const [reports,skipped,records]=await Promise.all([this.sections.reports?this.reports():undefined,this.sections.stuckFiles?callServer(this.transport,'GET /v1/admin/skipped'):undefined,this.sections.reports?this.records('runtime'):undefined]);if(!this.current())return;
    this.publish({...this.state,phase:'ready',reports:Object.freeze(reports?.items??[]),next:reports?.nextCursor,...(skipped?{setAside:{items:skipped.items,next:skipped.nextCursor}}:{}),...(records&&generation===this.recordsGeneration?{records:{lane:'runtime' as const,...records}}:{})});
   }catch{if(this.current())this.publish({...this.state,phase:'error'});}finally{this.reading=undefined;}});this.reading=task;return task;
 };
 more=async()=>{const cursor=this.state.next;if(!this.current()||this.reading||!cursor)return;this.publish({...this.state,phase:'loading'});try{const page=await this.reports(cursor);if(!this.current())return;const known=new Set(this.state.reports.map(row=>row.id));this.publish({...this.state,phase:'ready',reports:[...this.state.reports,...page.items.filter(row=>!known.has(row.id))],next:page.nextCursor});}catch{if(this.current())this.publish({...this.state,phase:'error'});}};
 async read(id:string,signal:AbortSignal,expected?:AppReport):Promise<AppReport&{log?:string}>{
  const selected=this.state.reports.find(row=>row.id===id);if(!selected||expected&&selected!==expected||!this.listed(selected)||signal.aborted)throw new DOMException('Report selection ended','AbortError');
  const report=await callServer(this.transport,'GET /v1/admin/reports/{id}',{id,signal});if(!this.listed(selected)||signal.aborted||report.id!==id)throw new DOMException('Report selection ended','AbortError');
  if(!report.hasLog)return report;if(!this.api.readReportLog)throw new Error('Report log transport is unavailable.');
  const log=await this.request(signal,current=>this.api.readReportLog!(id,current));if(!this.listed(selected)||signal.aborted)throw new DOMException('Report selection ended','AbortError');return {...report,log};
 }
 async remove(id:string,expected?:AppReport,caller?:Readonly<{signal:AbortSignal;isCurrent:()=>boolean}>):Promise<boolean>{
  const selected=this.state.reports.find(row=>row.id===id),admitted=()=>!caller||!caller.signal.aborted&&caller.isCurrent();if(!selected||expected&&selected!==expected||!this.listed(selected)||this.removing||!admitted())return false;
  const attempt={};this.removing=attempt;const owns=()=>this.current()&&this.removing===attempt&&admitted();this.publish({...this.state,phase:'loading'});
  try{if(!owns())return false;await callServer(this.transport,'DELETE /v1/admin/reports/{id}',{id,signal:caller?.signal});if(!owns())return false;const page=await this.reports('',caller?.signal);if(!owns())return false;this.publish({...this.state,phase:'ready',reports:page.items,next:page.nextCursor});return true;}
  catch{if(this.current())this.publish({...this.state,phase:'error'});return false;}finally{if(this.removing===attempt)this.removing=undefined;}
 }
 selectLane=async(lane:RecordLane)=>{if(!this.current()||!this.sections.reports)return;const generation=++this.recordsGeneration;try{const page=await this.records(lane);if(this.current()&&generation===this.recordsGeneration)this.publish({...this.state,records:{lane,...page}});}catch{if(this.current()&&generation===this.recordsGeneration)this.publish({...this.state,phase:'error'});}};
 moreRecords=async()=>{const shown=this.state.records;if(!this.current()||!shown?.next)return;const generation=++this.recordsGeneration;try{const page=await this.records(shown.lane,shown.next);if(this.current()&&generation===this.recordsGeneration&&this.state.records===shown)this.publish({...this.state,records:{lane:shown.lane,items:[...shown.items,...page.items],next:page.next}});}catch{if(this.current()&&generation===this.recordsGeneration)this.publish({...this.state,phase:'error'});}};
 latestRecords=async()=>{if(this.state.records)await this.selectLane(this.state.records.lane);};
 checkSetAside=async(more=false)=>{if(!this.current()||!this.sections.stuckFiles||more&&!this.state.setAside?.next)return;const shown=this.state.setAside;try{const page=await callServer(this.transport,'GET /v1/admin/skipped',{query:{cursor:more?shown?.next:undefined}});if(this.current()&&(!more||this.state.setAside===shown))this.publish({...this.state,setAside:{items:more?[...(shown?.items??[]),...page.items]:page.items,next:page.nextCursor}});}catch{if(this.current())this.publish({...this.state,phase:'error'});}};
 retry=()=>{this.state=empty;return this.load();};
 dispose(){if(this.lifetime.signal.aborted)return;this.lifetime.abort();this.recordsGeneration++;this.removing=undefined;this.state=empty;for(const listener of [...this.listeners])listener();this.listeners.clear();}
}
