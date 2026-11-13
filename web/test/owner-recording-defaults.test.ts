import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RecordingDefaults} from '../src/screens/settings/server-livetv/recording/RecordingDefaults.tsx';
test('recording defaults show backed padding and tuner conflict controls, with no removed conversion or folder templates',()=>{
 const snapshot={saving:false,loading:false,draft:{folder:'',beforeMs:60000,afterMs:180000,floorBytes:10*1024**3,capBytes:0,keepDays:0,keepNewest:0,stopViewers:false}};
 const form={subscribe:()=>()=>{},getSnapshot:()=>snapshot} as any;
 const html=[false,true].map(advanced=>renderToStaticMarkup(createElement(RecordingDefaults,{form,current:()=>true,advanced}))).join('');
 assert.match(html,/Start early/);assert.match(html,/Finish late/);assert.match(html,/Keep watching/);
 assert.doesNotMatch(html,/After recording|Remove the original|Folder layout|Folder for shows|Keep until watched/);
});
