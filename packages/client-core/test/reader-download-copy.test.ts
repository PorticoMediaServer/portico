import test from 'node:test';
import assert from 'node:assert/strict';
import {readerMessages} from '../../i18n/src/catalog/en-US/next/reader.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

test('reading download copy keeps the authored words and plain placeholders',()=>{
 assert.equal(readerMessages['next.reader.downloadTitle'],'Download {title}');
 assert.equal(readerMessages['next.reader.downloadSize'],'Download, {size}');
 assert.equal(readerMessages['next.reader.downloadEpubExplanation'],'Fits the screen and follows your type settings');
 assert.equal(readerMessages['next.reader.downloadPdfExplanation'],'The printed pages, fixed');
 assert.equal(defaultI18n.t('next.reader.downloadTitle',{title:'Dune'}),'Download Dune');
 assert.equal(defaultI18n.t('next.reader.downloadSize',{size:'1.9 MB'}),'Download, 1.9 MB');
 assert.equal(Object.hasOwn(readerMessages,'next.reader.downloadCbzExplanation'),false,'no CBZ explanation is invented');
});
