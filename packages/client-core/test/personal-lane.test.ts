import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeSeeAll} from '../src/home.ts';
import {setContainerState,undoContainerState} from '../src/container-state.ts';

test('Home destinations retain the exact library browse or list',()=>{
 assert.deepEqual(validateHomeSeeAll({type:'library',libraryId:'library',pivot:'shows',sort:[{field:'added',direction:'desc'}]}),{type:'library',libraryId:'library',pivot:'shows',sort:[{field:'added',direction:'desc'}]});
 assert.deepEqual(validateHomeSeeAll({type:'row',rowId:'continue'}),{type:'row',rowId:'continue'});
 assert.deepEqual(validateHomeSeeAll({type:'saved_view',resourceId:'view'}),{type:'saved_view',resourceId:'view'});
 assert.throws(()=>validateHomeSeeAll({type:'library',libraryId:'library',pivot:'movies',sort:[{field:'added',direction:'other'}]}));
 assert.throws(()=>validateHomeSeeAll({type:'row',rowId:''}));
});
