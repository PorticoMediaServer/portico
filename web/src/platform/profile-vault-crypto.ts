/** Two-layer browser proof envelope. The outer key is non-extractable and stored
 * by IndexedDB; the inner key requires the profile PIN. No PIN or account token
 * is persisted. Additional authenticated data binds the complete viewer scope. */
export type EncryptedProfileProof={version:1;salt:string;pinIV:string;deviceIV:string;cipher:string};
const bytes=(text:string)=>new TextEncoder().encode(text);
const source=(value:Uint8Array)=>value.buffer as ArrayBuffer;
function encode(value:ArrayBuffer|Uint8Array){const data=value instanceof Uint8Array?value:new Uint8Array(value);let out='';for(const byte of data)out+=String.fromCharCode(byte);return btoa(out);}
function decode(value:unknown,max:number){if(typeof value!=='string'||value.length>max)throw new Error('The protected proof is invalid.');const raw=atob(value);return Uint8Array.from(raw,c=>c.charCodeAt(0));}
async function pinKey(pin:string,salt:Uint8Array){
 const input=await crypto.subtle.importKey('raw',source(bytes(pin)),'PBKDF2',false,['deriveKey']);
 return crypto.subtle.deriveKey({name:'PBKDF2',hash:'SHA-256',salt:source(salt),iterations:200000},input,{name:'AES-GCM',length:256},false,['encrypt','decrypt']);
}
export function newBrowserProofKey():Promise<CryptoKey>{if(!globalThis.crypto?.subtle)throw new Error('Protected browser storage requires a secure browser context.');return crypto.subtle.generateKey({name:'AES-GCM',length:256},false,['encrypt','decrypt']);}
export async function encryptBrowserProof(key:CryptoKey,binding:string,payload:string,pin:string):Promise<EncryptedProfileProof>{
 if(payload.length>32768||binding.length>16384)throw new Error('The protected profile is too large.');
 const salt=crypto.getRandomValues(new Uint8Array(32)),pinIV=crypto.getRandomValues(new Uint8Array(12)),deviceIV=crypto.getRandomValues(new Uint8Array(12)),aad=source(bytes(binding));
 const inner=await crypto.subtle.encrypt({name:'AES-GCM',iv:source(pinIV),additionalData:aad},await pinKey(pin,salt),source(bytes(payload)));
 const cipher=await crypto.subtle.encrypt({name:'AES-GCM',iv:source(deviceIV),additionalData:aad},key,inner);
 return{version:1,salt:encode(salt),pinIV:encode(pinIV),deviceIV:encode(deviceIV),cipher:encode(cipher)};
}
export async function decryptBrowserProof(key:CryptoKey,binding:string,envelope:EncryptedProfileProof,pin:string):Promise<string>{
 if(envelope?.version!==1||binding.length>16384)throw new Error('The protected profile format is invalid.');
 const salt=decode(envelope.salt,64),pinIV=decode(envelope.pinIV,32),deviceIV=decode(envelope.deviceIV,32),cipher=decode(envelope.cipher,65536),aad=source(bytes(binding));
 if(salt.length!==32||pinIV.length!==12||deviceIV.length!==12||cipher.length<32)throw new Error('The protected proof is invalid.');
 const inner=await crypto.subtle.decrypt({name:'AES-GCM',iv:source(deviceIV),additionalData:aad},key,source(cipher));
 const plain=await crypto.subtle.decrypt({name:'AES-GCM',iv:source(pinIV),additionalData:aad},await pinKey(pin,salt),inner);
 if(plain.byteLength>32768)throw new Error('The protected proof is invalid.');return new TextDecoder('utf-8',{fatal:true}).decode(plain);
}
