import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../src/index.ts';

test('code-entry announcements pluralize the total without revealing entered digits',()=>{
 for(const [count,length,expected] of [[0,0,'0 of 0 digits'],[0,1,'0 of 1 digit'],[1,1,'1 of 1 digit'],[1,6,'1 of 6 digits'],[6,6,'6 of 6 digits']]){
  assert.equal(defaultI18n.t('auth.enteredDigits',{count,length}),expected);
 }
});

test('share-card joining instructions follow the server sign-in method',()=>{
 assert.equal(defaultI18n.t('accounts.invitation.shareJoinHelp',{method:'local'}),'Works once. Choose a username and password to join.');
 assert.equal(defaultI18n.t('accounts.invitation.shareJoinHelp',{method:'portico'}),'Works once. Join with your Portico Account.');
 assert.equal(defaultI18n.t('accounts.invitation.shareJoinHelp',{method:'unknown'}),'Works once.');
});

test('countdown and reused device/order messages retain their parameters',()=>{
 assert.equal(defaultI18n.t('accounts.signIn.retryIn',{time:'1:05'}),'Try again in 1:05');
 assert.equal(defaultI18n.t('next.cast.receiverOn',{platform:'Apple TV'}),'Portico on Apple TV');
 assert.equal(defaultI18n.t('web.group.ownOrder',{group:'playlist'}),'Playlist order');
 assert.equal(defaultI18n.t('web.group.ownOrder',{group:'collection'}),'Collection order');
});
