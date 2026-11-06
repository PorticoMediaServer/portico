// Generated once from already approved main spans; additions require a fresh
// explicit ruling. The closed grammar never learns from the live ledger.
import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';
const packet=JSON.parse(readFileSync(new URL('./client-api-legacy-forwarders.json',import.meta.url),'utf8'));
const hash=text=>createHash('sha256').update(text).digest('hex');
if(packet.sourceCommit!=='71a01131dad6460ea5bdfd095b635d6eaa898fea'||!Array.isArray(packet.sites))
  throw Error('Invalid frozen forwarder provenance');
const identities=new Map();
for(const s of packet.sites) {
  if(!s||typeof s.binding!=='string'||typeof s.file!=='string'||typeof s.owner!=='string'||typeof s.shape!=='string'
      ||!s.binding.startsWith(s.file+'#')||!Number.isSafeInteger(s.occurrences)||s.occurrences<1
      ||![undefined,'forwarding-function'].includes(s.guardSpan)||s.guardSpan==='forwarding-function'&&!s.forwardingHash
      ||!s.shape||![s.guardHash,s.callHash,...(s.forwardingHash?[s.forwardingHash]:[])].every(h=>/^[a-f0-9]{64}$/.test(h))
      ||typeof s.callText!=='string'||hash(s.callText)!==s.callHash)
    throw Error('Invalid frozen forwarder binding');
  const identity=JSON.stringify([s.file,s.guardSpan==='forwarding-function'?'forwarding-function':s.guardHash,s.forwardingHash??null,s.callHash,s.shape,s.owner]);
  if(identities.has(identity))throw Error('Duplicate frozen forwarder binding');
  identities.set(identity,Object.freeze(s));
}
export function legacyForwarderPin({file,guardHash,forwardingHash,callText,shape,owner}) {
  const policy=identities.get(JSON.stringify([file,guardHash,forwardingHash??null,hash(callText),shape,owner]));
  const callback=identities.get(JSON.stringify([file,'forwarding-function',forwardingHash??null,hash(callText),shape,owner]));
  if(policy&&callback)throw Error('Ambiguous frozen forwarder span');
  return policy??callback;
}
export function legacyForwarder(input) {
  return legacyForwarderPin(input)!==undefined;
}
