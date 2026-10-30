/** Pure source-clock mapping. Never infer a timestamp from receipt time, playlist
 * length, media-sequence, or currentTime alone. Gaps have no mapping. */
export type DatedFragment=Readonly<{start:number;duration:number;programDateTime:number|null;gap?:boolean}>;
export function channelMediaTime(originMs:number,positionSeconds:number,fragments:readonly DatedFragment[]):number|null{
 const date=originMs+positionSeconds*1000;if(!Number.isFinite(date))return null;
 const fragment=fragments.find(f=>!f.gap&&f.programDateTime!==null&&Number.isFinite(f.programDateTime)&&Number.isFinite(f.start)&&f.duration>0&&date>=f.programDateTime&&date<f.programDateTime+f.duration*1000);
 return fragment&&fragment.programDateTime!==null?fragment.start+(date-fragment.programDateTime)/1000:null;
}
export function channelSourceTime(originMs:number,dateMs:number|null):number|null{return dateMs!==null&&Number.isFinite(dateMs)&&Number.isSafeInteger(originMs)?(dateMs-originMs)/1000:null;}
