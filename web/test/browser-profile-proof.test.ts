import test from 'node:test';
import assert from 'node:assert/strict';
import {newBrowserProofKey,encryptBrowserProof,decryptBrowserProof} from '../src/platform/profile-vault-crypto.ts';
test('browser offline proof requires both device key and PIN, with exact scope authentication',async()=>{
 const device=await newBrowserProofKey(),other=await newBrowserProofKey();assert.equal(device.extractable,false);
 const scope='["hosted","account","server","profile","installation"]',payload='{"offlineProof":"synthetic-not-a-credential"}';
 const encrypted=await encryptBrowserProof(device,scope,payload,'0123');
 assert.equal(await decryptBrowserProof(device,scope,encrypted,'0123'),payload);
 await assert.rejects(decryptBrowserProof(device,scope,encrypted,'9999'));
 await assert.rejects(decryptBrowserProof(other,scope,encrypted,'0123'));
 await assert.rejects(decryptBrowserProof(device,scope+'another-server',encrypted,'0123'));
 assert.ok(!JSON.stringify(encrypted).includes('synthetic-not-a-credential'));assert.ok(!JSON.stringify(encrypted).includes('0123'));
});
test('browser proof authenticates ciphertext and validates envelope dimensions',async()=>{
 const key=await newBrowserProofKey(),record=await encryptBrowserProof(key,'scope','{}','');
 assert.equal(await decryptBrowserProof(key,'scope',record,''),'{}');
 await assert.rejects(decryptBrowserProof(key,'scope',{...record,cipher:(record.cipher[0]==='A'?'B':'A')+record.cipher.slice(1)},''));
 await assert.rejects(decryptBrowserProof(key,'scope',{...record,salt:'AA=='},''));
 await assert.rejects(decryptBrowserProof(key,'scope',{...record,version:2} as any,''));
});
