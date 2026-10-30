import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';

test('reading progress blocks removal with profile-specific recovery and no blind retry',()=>{
 for(const error of ['reading_progress_pending',{code:'reading_progress_pending',status:409,message:'private reading observations'}]){
  for(const deviceOnline of [true,false]){
   const result=presentError(error,'downloads',{operation:'action',deviceOnline,retrying:true});
   assert.equal(result.category,'unavailable');
   assert.equal(result.messageId,'error.readingProgressPending');
   assert.equal(result.body,'Connect as this profile and sync reading progress before removing this download.');
   assert.equal(result.action,'none');
   assert.equal(result.actionLabel,undefined);
   assert.equal(result.silent,false);
  }
 }
});
