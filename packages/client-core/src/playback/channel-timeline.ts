import {channelSeconds,type ChannelProjection} from './linear-protocol.ts';
/** Display-only projection. Clamping a slider thumb never changes the requested
 * source position or acknowledges a seek; the server validates every target. */
export function channelTimeline(linear:ChannelProjection,position:number){
 const m=linear.media,ready=!!m.streamUrl;
 const start=ready?channelSeconds(m.windowStartUs):0,end=ready?channelSeconds(m.windowEndUs):0,edge=ready?channelSeconds(m.liveEdgeUs):0;
 const span=Math.max(0,end-start),offset=Math.max(0,Math.min(span,position-start));
 const behind=Math.max(0,edge-position);
 const clock=(seconds:number)=>new Date(m.originMs+seconds*1000).toLocaleTimeString([], {hour:'numeric',minute:'2-digit',second:'2-digit'});
 return {start,end,edge,span,offset,ready:ready&&span>0,behind,clock,
  status:!ready?'Preparing channel':behind<=3?'Live':`${Math.ceil(behind)} seconds behind live`,
  gaps:m.gaps.map(g=>({start:channelSeconds(g.startUs),end:channelSeconds(g.endUs)}))};
}
