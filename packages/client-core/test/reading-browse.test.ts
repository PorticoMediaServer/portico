import test from 'node:test';
import assert from 'node:assert/strict';
import {validateContentEntry} from '../src/library-content.ts';
import {parseBrowseCapabilities} from '../src/browse.ts';

test('Reading browse leaves retain item navigation without audio playback semantics',()=>{
  for(const kind of ['ebook','comic']) {
    const raw={id:'reading-item',kind,title:'Real publication',libraryId:'reading',available:false,navigation:{view:'item',entityId:'reading-item'}};
    const entry=validateContentEntry(raw);
    assert.equal(entry.kind,kind);assert.equal(entry.navigation?.view,'item');assert.equal(entry.available,false);assert.equal(entry.playback,undefined);assert.equal(entry.duration,undefined);
    assert.throws(()=>validateContentEntry({...raw,playback:{itemId:'reading-item'}}));
    assert.throws(()=>validateContentEntry({...raw,durationMs:1000}));
  }
});

test('Reading Authors is a reachable facet surface rather than entity cards',()=>{
  const pivot={id:'authors',labelKey:'library.authors',entityKinds:[],defaultSort:[],supportedViews:['list'],browsable:false,sorts:[],filters:['author'],paging:'offset',facetSource:{endpoint:'/v1/libraries/{id}/facets',field:'author'}};
  const caps=parseBrowseCapabilities({library:{id:'reading',name:'Reading',kind:'book'},pivots:[pivot],resolvedPivot:pivot,fields:[],sorts:[],quickFilters:[],queryLimits:{maximumDepth:5,maximumClauses:40,maximumBytes:65536,maximumSorts:1,defaultLimit:40,maximumLimit:100,cursorTtlMs:1800000}});
  assert.equal(caps.resolvedPivot?.browsable,false);assert.equal(caps.resolvedPivot?.facetSource?.field,'author');assert.equal(caps.pivots[0].entityKinds.length,0);
});

test('Reading leaves retain a complete accepted author as a semantic card fact',()=>{
 const author='Accepted '+ 'a'.repeat(1000);
 for(const kind of ['ebook','comic']){
  const entry=validateContentEntry({id:'reading-item',kind,title:'Actual title',author,subtitle:author,navigation:{view:'item',entityId:'reading-item'}});
  assert.equal(entry.author,author);
 }
});
