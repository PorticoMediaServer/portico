import type {Measurement} from './console.ts';
export type Sample={at:number;value:number};
/** Keep gaps for unavailable readings, and never turn a counter reset into a negative rate. */
export function measurementSeries(readings:readonly Measurement[],key:string,rate?:'bytes'|'cpu'):Sample[]{
 const result:Sample[]=[];
 for(let i=0;i<readings.length;i++){
  const now=readings[i],fact=now.facts[key];if(fact?.state!=='available'||typeof fact.value!=='number'||!Number.isFinite(fact.value)||fact.value<0)continue;
  if(!rate){result.push({at:now.observedAt,value:fact.value});continue;}
  const before=readings[i-1],old=before?.facts[key];if(!before||old?.state!=='available'||typeof old.value!=='number'||fact.value<old.value)continue;
  const seconds=(now.observedAt-before.observedAt)/1000;if(seconds<=0||seconds>20)continue;
  result.push({at:now.observedAt,value:(fact.value-old.value)/seconds*(rate==='cpu'?100:8/1e6)});
 }
 return result;
}
export function appendMeasurement(readings:readonly Measurement[],next:Measurement):Measurement[]{
 if(readings.length&&next.observedAt<=readings[readings.length-1].observedAt)return [...readings];
 return [...readings.filter(v=>v.name===next.name&&next.observedAt-v.observedAt<=300000),next].slice(-61);
}
