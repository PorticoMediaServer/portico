import type {GetAdminPlayHistoryPeopleResponse} from '../../contracts/src/types.ts';

export type ReportContext=Readonly<{timeZone:string;until:string}>;
export type ReportInterval=Pick<GetAdminPlayHistoryPeopleResponse,'period'|'timeZone'|'from'|'until'|'buckets'>;
const QUARTER_MS=15*60*1000;
const counts:Readonly<Record<string,number>>={today:1,'7d':7,'30d':30,'90d':90,'1y':52,'12h':12,'24h':24};
const periods=new Set(['today','7d','30d','90d','1y','12h','24h','all','live','10m','1h']);
const instant=(v:unknown):v is string=>typeof v==='string'&&/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?(?:Z|[+-]\d{2}:\d{2})$/.test(v)&&Number.isFinite(Date.parse(v));
const object=(v:unknown):v is Record<string,any>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
function invalid():never{throw Object.assign(new Error('Invalid report interval.'),{code:'invalid_report_interval',retryable:false});}
function zone(value:unknown):value is string{
 if(typeof value!=='string'||value==='Local'||!/^[A-Za-z0-9_+\-/]{1,128}$/.test(value))return false;
 try{new Intl.DateTimeFormat('en',{timeZone:value});return true;}catch{return false;}
}
export function parseReportContext(value:unknown):ReportContext{
 if(!object(value)||!zone(value.timeZone)||!instant(value.until)||Date.parse(value.until)<0||Date.parse(value.until)%QUARTER_MS!==0)invalid();
 return Object.freeze({timeZone:value.timeZone,until:value.until});
}
/** Capture once per view/refresh, carrying this unchanged into full history. */
export function captureReportContext(timeZone:string,capturedAt:Date|number=new Date()):ReportContext{
 const at=typeof capturedAt==='number'?capturedAt:capturedAt.getTime();
 if(!Number.isFinite(at)||at<0)invalid();
 return parseReportContext({timeZone,until:new Date(Math.floor(at/QUARTER_MS)*QUARTER_MS).toISOString()});
}
export function reportQuery(context:ReportContext):URLSearchParams{
 const c=parseReportContext(context);return new URLSearchParams({timeZone:c.timeZone,until:c.until});
}
export function verifyReportInterval(interval:ReportInterval,context:ReportContext,period:string,live=false):void{
 const expected=parseReportContext(context);
 if(interval.period!==period||interval.timeZone!==expected.timeZone||!live&&Date.parse(interval.until)!==Date.parse(expected.until))invalid();
}
/** Validate producer bounds and identities; never resolve calendars on a client. */
export function parseReportInterval(value:unknown,live=false):ReportInterval{
 if(!object(value)||!zone(value.timeZone)||typeof value.period!=='string'||!periods.has(value.period)||!instant(value.from)||!instant(value.until)||!Array.isArray(value.buckets)||value.buckets.length>1201)invalid();
 const from=Date.parse(value.from),until=Date.parse(value.until);
 if(!Number.isFinite(from)||!Number.isFinite(until)||from<0||from>until||!live&&until%QUARTER_MS!==0)invalid();
 if(counts[value.period]!==undefined&&value.buckets.length!==counts[value.period])invalid();
 let end=from;
 for(let i=0;i<value.buckets.length;i++){
  const b=value.buckets[i];if(!object(b)||typeof b.partial!=='boolean'||!instant(b.start)||!instant(b.end))invalid();
  const a=Date.parse(b.start),z=Date.parse(b.end);
  if(!Number.isFinite(a)||!Number.isFinite(z)||a!==end||a>z||z>until||a===z&&(i!==value.buckets.length-1||!b.partial))invalid();
  end=z;
 }
 if(end!==until)invalid();
 return Object.freeze({period:value.period,timeZone:value.timeZone,from:value.from,until:value.until,buckets:value.buckets}) as ReportInterval;
}
