import {serviceText} from './presentation/service-text.ts';

/** What the viewer is told when the server ended their playback (a v1 session's `end`, a
 * `session.updated` payload, or a v2 occurrence's terminal error "terminated"). An administrator's
 * message is shown as written — it is meant for the viewer — within bounds and without control
 * characters; every other reason is the catalogue's sentence, in the viewer's language (W2-A11). */
export function sessionEndMessage(end:Readonly<{reason:string;message?:string}>):string {
 switch(end.reason){
  case 'terminated':{const text=(end.message??'').replace(/[\u0000-\u001f\u007f]+/g,' ').replace(/ {2,}/g,' ').trim().slice(0,500);return text||serviceText('player.ended.terminated');}
  case 'transferred':return serviceText('player.ended.transferred');
  // Live TV's handover sentence (LT-8), the same words its band uses.
  case 'tuner_reserved':return serviceText('live.handover.stopped');
  case 'lease_expired':return serviceText('player.ended.leaseExpired');
  case 'replaced':return serviceText('player.ended.replaced');
  case 'restored':return serviceText('player.ended.restored');
  case 'paused_timeout':return serviceText('player.ended.pausedTimeout');
  case 'outside_allowed_hours':return serviceText('player.ended.outsideHours');
  default:return serviceText('player.ended.other');
 }
}
