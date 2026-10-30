import test from 'node:test';
import assert from 'node:assert/strict';
import {metadataLanguage,metadataText} from '../src/epub/metadata.ts';

test('EPUB repeated dc:language selects one valid tag for metadata and document lang',()=>{
  assert.equal(metadataLanguage('en'),'en');
  assert.equal(metadataLanguage('zh-Hant'),'zh-Hant');
  assert.equal(metadataLanguage(['en','fr']),'en');
  assert.equal(metadataLanguage(['','fr-CA']),'fr-CA');
  assert.equal(metadataLanguage(['zh-Hant','en']),'zh-Hant');
  assert.equal(metadataLanguage([]),undefined);
  assert.equal(metadataLanguage(undefined),undefined);
});
test('EPUB metadata text preserves title and creator values',()=>{
  assert.equal(metadataText('Authored OPF & Navigation'),'Authored OPF & Navigation');
  assert.equal(metadataText(['Ada Lovelace','Grace Hopper']),'Ada Lovelace, Grace Hopper');
  assert.equal(metadataText({name:'Ada Lovelace'}),'Ada Lovelace');
  assert.equal(metadataText({name:{en:'Ada Lovelace',fr:'Ada'}}),'Ada Lovelace');
  assert.equal(metadataText(undefined),undefined);
});
