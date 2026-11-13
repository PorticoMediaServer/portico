const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path'),{execFileSync}=require('node:child_process');
const receiver=path.resolve(__dirname,'../..');
const source=fs.readFileSync(path.join(receiver,'cast/receiver-text.js'),'utf8');
function text(){const context={window:{}};vm.runInNewContext(source,context);return context.window.porticoCastText;}
test('receiver text is the deterministic canonical catalogue and formatter output',()=>{
 execFileSync(process.execPath,[path.join(receiver,'generate-cast-text.mjs'),'--check'],{stdio:'pipe'});
 const t=text().t;
 assert.equal(t('cast.receiver.idle.title'),'Ready to play from Portico');
 assert.equal(t('cast.receiver.pair.canceled'),'That code was canceled.');
 assert.equal(t('cast.receiver.episodeLabel',{season:0,episode:2}),'S0 E2');
 assert.equal(t('cast.receiver.error.unreachable.title'),'This TV can’t reach your server');
});
test('receiver substitution returns text for safe text nodes and preserves the emphasis marker',()=>{
 const t=text().t,marker='\uFFFC';
 assert.equal(t('cast.receiver.idle.hint',{playOn:marker}),`On your phone or computer, open Portico, choose ${marker}, and pick this TV.`);
 assert.equal(t('cast.receiver.takeover.title',{requester:'<img src=x onerror=alert(1)>'}),'<img src=x onerror=alert(1)> wants to play on this TV');
 assert.equal(t('missing.wire_label'),'');assert(Object.isFrozen(text()));
});
