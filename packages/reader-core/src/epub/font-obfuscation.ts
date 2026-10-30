import {sha1} from '@noble/hashes/legacy.js';

/** OCF's legacy font-obfuscation key, not an integrity/security hash.
 * https://www.w3.org/publishing/epub32/epub-ocf.html#sec-resource-obfuscation
 * The engine accepts this portable injection on ordinary self-host HTTP. */
export function epubFontKey(identifier:string):Uint8Array {
  return sha1(new TextEncoder().encode(identifier.replace(/[\u0020\u0009\u000d\u000a]/g,'')));
}
