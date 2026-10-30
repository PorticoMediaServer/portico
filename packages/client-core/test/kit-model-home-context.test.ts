import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {collectionCaptionParts, titleFactParts, type FactPart, type KitTranslate} from '../src/presentation/kit-model/index.ts';

const t:KitTranslate=(id,...args)=>formatMessage(shared[id],args[0]);
const env={t,locale:'en-US'};
const parts=(values:readonly FactPart[])=>values.map(value=>[value.id,value.text,value.role]);

// mock#1 :332–364 and phone-app#1@1 :559: year, genres, then duration/season tag.
test('Home films use year then provider-order genres then duration, with no rating or season tag',()=>{
  const facts={year:2001,genres:['Animation','Family'],durationSeconds:7500,contentRating:'PG',seasonCount:8};
  assert.deepEqual(parts(titleFactParts(facts,env,'home-movie')),[
    ['year','2001','value'],['genres','Animation, Family','supplement'],['duration','2 h 5 min','value'],
  ]);
  assert.deepEqual(parts(titleFactParts(facts,env,'video')),[
    ['year','2001','value'],['duration','2 h 5 min','value'],['genre-0','Animation','supplement'],['genre-1','Family','supplement'],['content-rating','PG','supplement'],['season-count','8 seasons','supplement'],
  ],'the explicit Home context does not rewrite generic detail/browse video facts');
});

// mock#1 :332 and tv#1 :383–385: show metadata has seasons, never episode length.
test('Home and TV show context keeps year, genres and real seasons without episode duration or rating',()=>{
  assert.deepEqual(parts(titleFactParts({year:2023,genres:['Drama'],seasonCount:1,durationSeconds:4900,contentRating:'TV-MA'},env,'home-show')),[
    ['year','2023','value'],['genres','Drama','supplement'],['season-count','1 season','supplement'],
  ]);
  assert.deepEqual(parts(titleFactParts({year:2022,genres:['Drama','Mystery','Sci-Fi'],seasonCount:2},env,'home-show')),[
    ['year','2022','value'],['genres','Drama, Mystery, Sci-Fi','supplement'],['season-count','2 seasons','supplement'],
  ]);
  assert.deepEqual(titleFactParts({durationSeconds:4900,contentRating:'TV-MA'},env,'home-show'),[]);
  assert.deepEqual(titleFactParts({seasonCount:1,contentRating:'PG'},env,'home-movie'),[]);
});

test('Home omissions retain the surviving semantic order and arbitrary provider punctuation',()=>{
  const genre='A long provider genre · with punctuation';
  for(const context of ['home-movie','home-show'] as const){
    assert.deepEqual(parts(titleFactParts({genres:['',genre,'  ','Mystery']},env,context)),[
      ['genres',`${genre}, Mystery`,'supplement'],
    ]);
    for(const invalid of [undefined,0,-1,NaN,Infinity,-Infinity,1.5]){
      assert.deepEqual(titleFactParts({year:invalid,genres:[' '],durationSeconds:undefined,seasonCount:invalid},env,context),[]);
    }
  }
  for(const invalid of [undefined,0,-1,NaN,Infinity,-Infinity])assert.deepEqual(titleFactParts({durationSeconds:invalid},env,'home-movie'),[]);
  assert.deepEqual(parts(titleFactParts({durationSeconds:45},env,'home-movie')),[['duration','45 s','value']]);
  const result=titleFactParts({year:2024,genres:['Drama']},env,'home-show');
  assert.ok(Object.isFrozen(result));
  assert.ok(result.every(Object.isFrozen));
});

// mock#1 :427–435: this is a complete sentence, not artwork-preview statistics.
test('Home collection uses the exact drawn film sentence from authoritative count and endpoints',()=>{
  assert.deepEqual(parts(collectionCaptionParts({mediaKind:'movie',itemCount:4,firstYear:1995,lastTitle:'Toy Story 5'},env)),[
    ['collection-summary','Four films on your server, from 1995 to Toy Story 5.','context'],
  ]);
  assert.deepEqual(parts(collectionCaptionParts({mediaKind:'movie',itemCount:1,firstYear:2001,lastTitle:'A supplied title'},env)),[
    ['collection-summary','One film on your server, from 2001 to A supplied title.','context'],
  ]);
  assert.equal(collectionCaptionParts({mediaKind:'movie',itemCount:12,firstYear:1901,lastTitle:'A long final title · unchanged'},env)[0]?.text,
    '12 films on your server, from 1901 to A long final title · unchanged.');
});

test('collection caption omits incomplete ranges without guessing endpoints or media kind',()=>{
  for(const range of [{},{firstYear:1995},{lastTitle:'Last title'},{firstYear:NaN,lastTitle:'Last title'},{firstYear:0,lastTitle:'Last title'},{firstYear:1995.5,lastTitle:'Last title'},{firstYear:1995,lastTitle:'  '}]){
    assert.deepEqual(parts(collectionCaptionParts({mediaKind:'movie',itemCount:4,...range},env)),[
      ['collection-summary','Four films on your server.','context'],
    ]);
  }
  assert.equal(collectionCaptionParts({mediaKind:'movie',itemCount:1},env)[0]?.text,'One film on your server.');
  assert.equal(collectionCaptionParts({mediaKind:'movie',itemCount:2},env)[0]?.text,'2 films on your server.');
  for(const mediaKind of [undefined,'','show','book','music','Movie'])assert.deepEqual(collectionCaptionParts({mediaKind,itemCount:4,firstYear:1995,lastTitle:'Last title'},env),[]);
  for(const itemCount of [undefined,0,-1,NaN,Infinity,-Infinity,1.5])assert.deepEqual(collectionCaptionParts({mediaKind:'movie',itemCount,firstYear:1995,lastTitle:'Last title'},env),[]);
  const result=collectionCaptionParts({mediaKind:'movie',itemCount:4},env);
  assert.ok(Object.isFrozen(result));assert.ok(Object.isFrozen(result[0]));
});
