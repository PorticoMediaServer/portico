import {serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';
import {sessionEndMessage} from './session-end.ts';
export {sessionEndMessage};
/** Presentation only: recovery and authorization decisions still use the original failure. */
export function playbackUserMessage(error:unknown):string {
 const value=error&&typeof error==='object'?error as {code?:unknown;message?:unknown;status?:unknown}:undefined;
 const code=typeof value?.code==='string'?value.code:typeof error==='string'?error:'';
 if(code==='no_admissible_delivery_route')return serviceText('player.cannotPlay.serverConversionUnavailable');
 // Each known failure has its sentence in the catalogue (`playback.message.*`), in the viewer's language.
 const known:ReadonlySet<string>=new Set(['unauthorized','access_revoked','forbidden','source_unavailable','storage_offline','source_changed','not_found','owner_account_cap','owner_server_cap','transcoding_disabled','unsupported_tuple','unknown_source_facts','encoder_failure','actual_allocation_failure','decoder_contradiction','delivery_http_failure','network_stall','timeout','controller_retired','lease_expired','candidate_expired','revision_conflict','identity_conflict','sequence_stale','receipt_expired','receipt_not_found','lane_occupied','operation_unavailable','seek_mismatch','timeline_unmapped','unsupported_version','invalid_request','internal_error']);
 const say=(name:string)=>serviceText(('playback.message.'+name) as MessageId);
 if(code==='terminated')return sessionEndMessage({reason:'terminated',message:typeof value?.message==='string'?value.message:undefined});
 if(code==='conversion_capacity')return serviceText('error.playbackConversionCapacity');
 if(code==='queue_too_large')return serviceText('error.queueTooLarge');
 // Lyric and subtitle searches on a library whose metadata source is local only.
 if(code==='local_metadata_only')return serviceText('error.localMetadataOnly');
 if(known.has(code))return say(code);
 if(value?.status===401)return say('unauthorized');
 if(value?.status===403)return say('forbidden');
 // Do not echo arbitrary server text, URLs, IDs, native exceptions or serialized payloads.
 const message=typeof error==='string'?error:typeof value?.message==='string'?value.message:'';
 const safe=[/^This title can’t be played on this device\.$/,/^This title needs converting, and converting is turned off for this account\.$/,/^This item isn’t available\.$/,/^This account is already playing as many streams as it is allowed\.$/,/^This queue is too long\. Remove some of it or start a new one\.$/,/^This part of the queue is still loading\. Try again in a moment\.$/,/^The queue has ended\.$/,/^Listening timer paused playback\.$/,/^Playback has ended on the server\.$/,/^Choose Go Live or a position in the retained window before resuming\.$/,/^The channel stream ended\. Retry or choose Go Live\.$/,/^The previous playback could not be stopped\. Retry this channel when the server reconnects\.$/];
 if(message==='Playback control disconnected. Retrying…')return say('reconnectingControls');
 if(message==='This media could not be played. Your playback position is retained.')return say('positionKept');
 if(safe.some(pattern=>pattern.test(message)))return message;
 if(error instanceof TypeError||/network|fetch|connection|timed? ?out/i.test(message))return say('connection');
 return say('generic');
}

/**
 * Whether trying again cannot help: the title cannot be played on this device at all, or this
 * viewer may not play it. The player then says so plainly instead of offering "Try again" as
 * the way forward. Everything else (the network, a busy server, a stream that was ended) is
 * worth another try.
 */
export function playbackFailurePermanent(error:unknown):boolean {
 const value=error&&typeof error==='object'?error as {code?:unknown;message?:unknown}:undefined;
 const text=typeof error==='string'?error:typeof value?.code==='string'?value.code:typeof value?.message==='string'?value.message:'';
 return ['no_admissible_delivery_route','cannot_play_here','streaming_unsupported','unsupported_media','unsupported_tuple','unknown_source_facts','transcoding_disabled','transcode_not_allowed','not_found','forbidden','access_revoked','unsupported_version','client_update_required',
  'This title can’t be played on this device.','This title needs converting, and converting is turned off for this account.','This item isn’t available.'].includes(text);
}
