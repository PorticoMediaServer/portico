import assert from 'node:assert/strict';
import {test} from 'node:test';
import {epubDisplay,type EpubDisplayPreferences} from '../src/epub/display.ts';
// Engine projection checks, not real-book setting effect or visual acceptance.
const p:EpubDisplayPreferences={theme:'dark',font:'literata',textSize:5,lineSpacing:'normal',margins:'normal',alignment:'justified',layout:'pages',twoPages:true,hyphenation:true,tapToTurn:true,highlightColour:'yellow'};
const fonts={literata:'blob:trusted-literata',manrope:'blob:trusted-manrope'},wide={width:1440,height:900};
test('approved palette/font/size/spacing/alignment/hyphenation choices reach the engine style projection',()=>{
  const initial=epubDisplay(p,wide,fonts);
  assert.equal(initial.columns,2);assert.match(initial.css,/font-size:20px/);assert.match(initial.css,/line-height:1.6/);assert.match(initial.css,/hyphens:auto/);
  for(const theme of ['black','sepia','light'] as const)assert.notEqual(epubDisplay({...p,theme},wide,fonts).background,initial.background);
  assert.match(epubDisplay({...p,font:'manrope'},wide,fonts).css,/font-family:Manrope,sans-serif!important/);
  assert.doesNotMatch(epubDisplay({...p,font:'publisher'},wide,fonts).css,/font-family:Literata,serif!important/);
  assert.match(epubDisplay({...p,textSize:7,lineSpacing:'loose',alignment:'left',hyphenation:false},wide,fonts).css,/font-size:26px/);
  assert.match(epubDisplay({...p,hyphenation:false},wide,fonts).css,/hyphens:none/);
  assert.notEqual(epubDisplay({...p,margins:'wide'},wide,fonts).margin,initial.margin);
  assert.notEqual(epubDisplay({...p,highlightColour:'pink'},wide,fonts).css,initial.css);
});
test('EPUB rotation, single page choice and scrolled flow preserve declared sizing rules',()=>{
  assert.equal(epubDisplay(p,{width:844,height:390},fonts).columns,2);
  assert.equal(epubDisplay(p,{width:820,height:1180},fonts).columns,1);
  assert.equal(epubDisplay({...p,twoPages:false},wide,fonts).columns,1);
  assert.equal(epubDisplay({...p,layout:'scroll'},wide,fonts).flow,'scrolled');
  assert.throws(()=>epubDisplay({...p,textSize:8},wide,fonts));
});
test('default text geometry follows exact desktop, phone and tablet reader frames',()=>{
  const desktop=epubDisplay(p,wide,fonts);assert.equal(desktop.margin,150);assert.equal(desktop.top,100);assert.equal(desktop.bottom,96);
  const phone=epubDisplay(p,{width:390,height:844},fonts);assert.equal(phone.margin,24);assert.equal(phone.top,86);assert.match(phone.css,/font-size:17px/);
  const sideways=epubDisplay(p,{width:844,height:390},fonts);assert.equal(sideways.margin,56);assert.equal(sideways.top,40);assert.match(sideways.css,/font-size:16px/);
  const tablet=epubDisplay(p,{width:1180,height:820},fonts);assert.equal(tablet.margin,76);assert.equal(tablet.top,100);assert.equal(tablet.bottom,112);
  const beside=epubDisplay(p,{width:988,height:900,turnClearance:120},fonts);assert.equal(beside.columns,1);assert.equal(beside.margin,120);
  // W07 round 4: beside the open panel the text area is narrower than the window, but the arrows are still drawn.
  const panel1024=epubDisplay(p,{width:572,height:900,turnClearance:120},fonts);assert.equal(panel1024.margin,120);
  const panel1280=epubDisplay(p,{width:828,height:900,turnClearance:120},fonts);assert.equal(panel1280.margin,120);
  const noArrows=epubDisplay(p,{width:988,height:900},fonts);assert.equal(noArrows.margin,84);
  const roomy=epubDisplay(p,{width:1440,height:900,turnClearance:120},fonts);assert.equal(roomy.margin,150);
  const upright=epubDisplay(p,{width:820,height:1180},fonts);assert.equal(upright.margin,84);assert.equal(upright.bottom,120);
});
