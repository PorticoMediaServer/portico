import {requireTag} from './needs-newer-app.ts';
import {unreadableServerResponse} from './server-messages.ts';

/** The setup result is tagged `variant` `session` (a signed-in owner) or `claim-required` (a resumable Portico
 * Account setup). A tag this app does not know is a newer server (`NeedsNewerAppError`). */
export function setupResultBody(raw:unknown):Readonly<{tag:'session'|'claim-required';body:Record<string,unknown>}>{
 const tag=requireTag(raw,'variant',['session','claim-required'],()=>{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_current_session'});}) as 'session'|'claim-required';
 const {variant:_variant,...body}=raw as Record<string,unknown>;
 return {tag,body};
}
