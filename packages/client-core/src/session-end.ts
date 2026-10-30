/** What the viewer is told when the server ended their playback (a v1 session's `end`, a
 * `session.updated` payload, or a v2 occurrence's terminal error "terminated"). An administrator's
 * message is shown as written — it is meant for the viewer — within bounds and without control
 * characters; every other reason gets fixed text. */
export function sessionEndMessage(end:Readonly<{reason:string;message?:string}>):string {
 switch(end.reason){
  case 'terminated':{const text=(end.message??'').replace(/[\u0000-\u001f\u007f]+/g,' ').replace(/ {2,}/g,' ').trim().slice(0,500);return text||'The server owner stopped this playback.';}
  case 'transferred':return 'Playback continued on another device.';
  case 'tuner_reserved':return 'Live TV stopped because a recording needed this tuner.';
  case 'lease_expired':return 'The stream disconnected. Play again to continue.';
  case 'replaced':return 'This playback was replaced by a newer one.';
  case 'restored':return 'The server was restored from a backup. Play again to continue.';
  case 'paused_timeout':return 'Playback stopped after a long pause.';
  case 'outside_allowed_hours':return 'This account can’t be used at this time of day, so playback stopped. Try again during its allowed hours.';
  default:return 'Playback was stopped on another device.';
 }
}
