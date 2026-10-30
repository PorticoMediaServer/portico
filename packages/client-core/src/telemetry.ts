import {parseReportInterval,type ReportInterval} from './report-context.ts';
/**
 * Owner telemetry and transcode capacity, as the selected server publishes
 * them. Every metric carries its own status, so a client renders "not
 * available on this platform" instead of drawing a zero the server never
 * measured. Nothing here is inferred on the client.
 *
 * A server may be newer than this app: a metric, point, probe, preset, tool or warning that cannot be
 * read is left out (a metric then reads as unavailable) and the rest is kept. Sizes, session counts
 * and limits stay exact.
 */
import {keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
export type MetricStatus='available'|'limited'|'unavailable';
export type Metric={status:MetricStatus;value:number;detail?:string};
export type MetricName='cpu'|'memory'|'diskRead'|'diskWrite'|'netIn'|'netOut'|'gpuUsage'|'gpuMemory'|'gpuEncoder'|'processCPU'|'processMemory'|'homeMbps'|'awayMbps'|'hardwareConversions'|'softwareConversions';
export const METRIC_NAMES:MetricName[]=['cpu','memory','diskRead','diskWrite','netIn','netOut','gpuUsage','gpuMemory','gpuEncoder','processCPU','processMemory','homeMbps','awayMbps','hardwareConversions','softwareConversions'];
export type TelemetryWindow='today'|'live'|'12h'|'24h'|'7d'|'30d'|'90d'|'1y'|'all'|'10m'|'1h';
export const TELEMETRY_WINDOWS:TelemetryWindow[]=['today','live','12h','24h','7d','30d','90d','1y','all','10m','1h'];
export type TelemetrySample={at:number;metrics:Record<MetricName,Metric>;memoryUsedBytes:number;memoryTotalBytes:number;gpuDevice?:string;gpuProvider?:string};
/** Per-point producer provenance. Older/malformed status remains unknown;
 * neither a numeric zero nor the series' newest status can supply it. */
export type TelemetryPoint={t:number;v:number;status?:'available'|'limited';detail?:string};
export type TelemetryReading=ReportInterval & {window:TelemetryWindow;series:Record<MetricName,TelemetryPoint[]>;status:Record<MetricName,Metric>;observedAt:number};

export type HardwareProbe={backend:string;supported:boolean;detail:string;device?:string;encoder?:string};
/** One adapter the server found, named as its driver names it. `method` is a stable token. */
export type HardwareDeviceFact={id:string;name:string;method?:string;inUse:boolean};
/** Whether the backend playback selected is qualified to do one format's job. */
export type HardwareFormatFact={format:string;decode:boolean;encode:boolean};
/** The one failure worth reporting: shown only when no hardware backend works. */
export type HardwareFault={backend?:string;device?:string;reason:string;detail?:string};
export type HardwareStatus={configured:string;effective:string;device:string;probes:HardwareProbe[];
 /** Adapters and the measured grid; empty when the server publishes no inventory. */
 devices:HardwareDeviceFact[];formats:HardwareFormatFact[];lastError?:HardwareFault;
 /** When the newest probe finished, in Unix seconds; zero means nothing has been probed yet. */
 lastTest:number};
export type SessionLimits={concurrent:number;hardware:number;software:number;background:number};
/** A null count means the server publishes no figure, never "no sessions". */
export type SessionCounts={active:number|null;hardware:number|null;software:number|null;background:number|null;limits:SessionLimits};
export type ToneMappingStatus={status:'disabled'|'available'|'limited'|'unavailable';detail:string};
export type TemporaryDirectory={path:string;ready:boolean;freeBytes:number|null;
 /** No owner directory is set and the server's own state directory is used. */
 fallback:boolean;
 /** Candidate RAM file system; independent of the disk folder and owner preference. */
 memoryPath:string;
 /** Playback admission for this host and the configured memory cap. Missing facts mean unavailable. */
 memoryAvailable:boolean};
export type TranscodePreset={id:string;label:string;height:number;videoKbps:number;audioKbps:number};
export type RuntimeDependency={path:string;version:string;ok:boolean};
export type TranscodeCapacity={
 enabled:boolean;hardware:HardwareStatus;sessions:SessionCounts;hdrToneMapping:ToneMappingStatus;
 directStreamRemux:boolean;x264Preset:string;planningPolicy:string;throttleBufferMs:number;playedRetentionMs:number;
 temporaryDirectory:TemporaryDirectory;presets:TranscodePreset[];dependencies:Record<string,RuntimeDependency>;warnings:string[];observedAt:number};

const obj=(v:unknown):v is Record<string,any>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const finite=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v);
const text=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max;
function invalid():never{throw new Error('Invalid selected-server telemetry response.');}
function base(v:unknown):Record<string,any>{if(!obj(v))invalid();return v;}

export function parseMetric(value:unknown):Metric{
 const m=base(value);
 if(!finite(m.value)||m.detail!==undefined&&!text(m.detail,512))invalid();
 // A status this app does not know reads as unavailable, so a value it cannot vouch for is never drawn.
 if(!['available','limited','unavailable'].includes(m.status)){noteSkipped('telemetry','metric.status');return {status:'unavailable',value:0,...(m.detail===undefined?{}:{detail:m.detail})};}
 if(m.status==='unavailable'&&m.value!==0)invalid();
 return m as Metric;
}
function metricMap(value:unknown):Record<MetricName,Metric>{
 const source=base(value);const out={} as Record<MetricName,Metric>;
 // A metric that is missing or cannot be read is shown as unavailable; the others are kept.
 for(const name of METRIC_NAMES){try{out[name]=parseMetric(source[name]);}catch{noteSkipped('telemetry','metric');out[name]={status:'unavailable',value:0};}}
 return out;
}
export function parseTelemetrySample(value:unknown):TelemetrySample{
 const s=base(value);
 if(!integer(s.at)||!integer(s.memoryUsedBytes)||!integer(s.memoryTotalBytes)||s.memoryUsedBytes>s.memoryTotalBytes&&s.memoryTotalBytes!==0)invalid();
 // A device or provider name that cannot be read is left out.
 const named:Record<string,any>={...s};
 for(const key of ['gpuDevice','gpuProvider'] as const)if(named[key]!==undefined&&!text(named[key],200)){noteSkipped('telemetry',key);delete named[key];}
 return {...named,metrics:metricMap(s.metrics)} as TelemetrySample;
}
/** A window holds at most one point every two seconds for ten minutes. */
const MAX_POINTS=1600;
export function parseTelemetryReading(value:unknown):TelemetryReading{
 const r=base(value);
 if(!TELEMETRY_WINDOWS.includes(r.window)||!integer(r.observedAt)||r.period!==r.window)invalid();
 const interval=parseReportInterval(r,r.window==='live'||r.window==='10m');
 const bucketStarts=new Set(interval.buckets.map(b=>Date.parse(b.start)));
 const series=base(r.series);const out={} as Record<MetricName,TelemetryPoint[]>;
 for(const name of METRIC_NAMES){
  const points=series[name] ?? (METRIC_NAMES.indexOf(name)>=9?[]:undefined);
  if(!Array.isArray(points)||points.length>MAX_POINTS)invalid();
  let previous=-1;
  // A point that is out of order, from the future or not a number is left out; the rest of the line is kept.
  out[name]=keepReadable('telemetry','series.points',points,point=>{
   const p=base(point);
   if(!integer(p.t)||!finite(p.v)||p.t<=previous||p.t>r.observedAt)invalid();
   if(r.window!=='live'&&r.window!=='10m'&&(!bucketStarts.has(p.t)||p.t>=Date.parse(interval.until)))invalid();
   previous=p.t;
   const status=p.status==='available'||p.status==='limited'?p.status:undefined;
   if(p.status!==undefined&&status===undefined)noteSkipped('telemetry','point.status');
   const detail=text(p.detail,512)?p.detail:undefined;
   if(p.detail!==undefined&&detail===undefined)noteSkipped('telemetry','point.detail');
   return {t:p.t,v:p.v,...(status===undefined?{}:{status}),...(detail===undefined?{}:{detail})};
  });
 }
 return {...interval,window:r.window,series:out,status:metricMap(r.status),observedAt:r.observedAt};
}

export function parseTranscodeCapacity(value:unknown):TranscodeCapacity{
 const c=base(value);
 if(typeof c.enabled!=='boolean'||typeof c.directStreamRemux!=='boolean'||!text(c.x264Preset,40)||!text(c.planningPolicy,40)||
  !integer(c.throttleBufferMs)||!integer(c.playedRetentionMs)||!integer(c.observedAt))invalid();
 const hardware=base(c.hardware);
 if(!text(hardware.configured,40)||!text(hardware.effective,40)||!text(hardware.device,200)||!Array.isArray(hardware.probes)||hardware.probes.length>20)invalid();
 const probes=keepReadable('transcode-capacity','hardware.probes',hardware.probes,probe=>{const p=base(probe);if(!text(p.backend,40)||typeof p.supported!=='boolean'||!text(p.detail,512))invalid();return p as HardwareProbe;});
 // A device, a format row or a fault this app cannot read is left out; the rest of the page stays.
 const devices=Array.isArray(hardware.devices)?keepReadable('transcode-capacity','hardware.devices',hardware.devices.slice(0,32),device=>{
  const d=base(device);if(!text(d.id,128)||!d.id||!text(d.name,200)||!d.name||typeof d.inUse!=='boolean'||d.method!==undefined&&!text(d.method,40))return undefined;
  return {id:d.id as string,name:d.name as string,...(d.method===undefined?{}:{method:d.method as string}),inUse:d.inUse as boolean};
 }):[];
 const formats=Array.isArray(hardware.formats)?keepReadable('transcode-capacity','hardware.formats',hardware.formats.slice(0,16),row=>{
  const f=base(row);if(!text(f.format,32)||!f.format||typeof f.decode!=='boolean'||typeof f.encode!=='boolean')return undefined;
  return {format:f.format as string,decode:f.decode as boolean,encode:f.encode as boolean};
 }):[];
 let lastError:HardwareFault|undefined;
 if(obj(hardware.lastError)){
  const f=hardware.lastError;
  if(text(f.reason,64)&&f.reason&&(f.backend===undefined||text(f.backend,40))&&(f.device===undefined||text(f.device,200))&&(f.detail===undefined||text(f.detail,512)))
   lastError={reason:f.reason as string,...(f.backend===undefined?{}:{backend:f.backend as string}),...(f.device===undefined?{}:{device:f.device as string}),...(f.detail===undefined?{}:{detail:f.detail as string})};
  else noteSkipped('transcode-capacity','hardware.lastError');
 }
 const lastTest=integer(hardware.lastTest)?hardware.lastTest:0;
 const sessions=base(c.sessions);
 for(const key of ['active','hardware','software','background'])if(sessions[key]!==null&&!integer(sessions[key]))invalid();
 const limits=base(sessions.limits);
 for(const key of ['concurrent','hardware','software','background'])if(!integer(limits[key]))invalid();
 const tone=base(c.hdrToneMapping);
 if(!text(tone.detail,512))invalid();
 // A tone-mapping status this app does not know reads as unavailable; the server's own detail is still shown.
 const toneStatus=oneOfOr('transcode-capacity','hdrToneMapping.status',tone.status,['disabled','available','limited','unavailable'] as const,'unavailable');
 const directory=base(c.temporaryDirectory);
 if(!text(directory.path,1024)||typeof directory.ready!=='boolean'||directory.freeBytes!==null&&!integer(directory.freeBytes))invalid();
 // Missing or malformed admission facts mean unavailable, even if a candidate path exists.
 const directoryFacts={fallback:directory.fallback===true,memoryAvailable:directory.memoryAvailable===true&&text(directory.memoryPath,1024)&&!!directory.memoryPath,...(text(directory.memoryPath,1024)&&directory.memoryPath?{memoryPath:directory.memoryPath as string}:{memoryPath:''})};
 if(!Array.isArray(c.presets)||c.presets.length>20)invalid();
 const presets=keepReadable('transcode-capacity','presets',c.presets,preset=>{const p=base(preset);if(!text(p.id,40)||!text(p.label,80)||!integer(p.height)||!integer(p.videoKbps)||!integer(p.audioKbps))invalid();return p as TranscodePreset;});
 const published=base(c.dependencies);
 if(Object.keys(published).length>8)invalid();
 const dependencies:Record<string,RuntimeDependency>={};
 for(const [name,dependency] of Object.entries(published)){if(obj(dependency)&&text(dependency.path,1024)&&text(dependency.version,512)&&typeof dependency.ok==='boolean')dependencies[name]=dependency as RuntimeDependency;else noteSkipped('transcode-capacity','dependencies');}
 if(!Array.isArray(c.warnings)||c.warnings.length>20)invalid();
 const warnings=keepReadable('transcode-capacity','warnings',c.warnings,(w:unknown)=>text(w,512)?w:undefined);
 return {...c,hardware:{...hardware,probes,devices,formats,lastError,lastTest},temporaryDirectory:{...directory,...directoryFacts},hdrToneMapping:{...tone,status:toneStatus},presets,dependencies,warnings} as TranscodeCapacity;
}
