import test from 'node:test';
import assert from 'node:assert/strict';
import {fixedPageViewport} from '../src/pdf/fixed-view.ts';

// Geometry only: this exercises the pure projection that the fixed view
// publishes on `ReaderLocation.viewport`. Mounting needs a real DOM, so the
// scroll/zoom wiring itself stays with the browser reader harness.
const page={pageWidth:600,pageHeight:800},viewport={width:1000,height:700};
const at=(left:number,top:number,scale=2,fittedScale=1)=>
  fixedPageViewport({...page,page:0,scale,fittedScale,scroll:{left,top,width:viewport.width,height:viewport.height},viewport});
const edge=(value:number)=>Math.abs(value-Math.round(value*1e6)/1e6)<1e-9;

test('a page at or below its fitted scale publishes no viewport',()=>{
  assert.equal(at(0,0,1,1),undefined);
  assert.equal(at(0,0,.8,1),undefined);
  // A fit preference keeps the admitted scale equal to the fitted one.
  assert.equal(fixedPageViewport({...page,page:3,scale:1.333333333,fittedScale:1.333333333,scroll:{left:40,top:90,width:800,height:600},viewport:{width:800,height:600}}),undefined);
});

test('pan clamps the visible rectangle at every page edge',()=>{
  const topLeft=at(0,0);
  assert.ok(topLeft);
  assert.equal(topLeft.x,0);assert.equal(topLeft.y,0);
  assert.equal(topLeft.width,1000/1200);assert.equal(topLeft.height,700/1600);
  // The scroll range of a 1200x1600 page in a 1000x700 box.
  const bottomRight=at(200,900);
  assert.ok(bottomRight);
  assert.equal(bottomRight.x,200/1200);assert.equal(bottomRight.y,900/1600);
  assert.ok(edge(bottomRight.x+bottomRight.width-1)&&edge(bottomRight.y+bottomRight.height-1),'reaches the page edges');
  // Overscroll past the real range can only clamp to the page.
  const past=at(500,5000);
  assert.ok(past);
  assert.equal(past.width,1-500/1200);assert.equal(past.x+past.width,1);
  assert.equal(past.height,0);assert.equal(past.y,1);
  // A rubber-band overscroll behind the origin never leaves the page.
  const before=at(-120,-80);
  assert.ok(before);
  assert.equal(before.x,0);assert.equal(before.y,0);
});

test('the rectangle always stays inside the page',()=>{
  for(const pageWidth of [300,600,1414])for(const pageHeight of [200,800,2000])
    for(const scale of [.3,1,1.75,3.5,8])for(const left of [-200,-1,0,37,250,900,4000])for(const top of [-50,0,15,480,3000]){
      const value=fixedPageViewport({page:7,pageWidth,pageHeight,scale,fittedScale:.2,scroll:{left,top,width:viewport.width,height:viewport.height},viewport});
      assert.ok(value,'a zoomed page always reports geometry');
      for(const fraction of [value.x,value.y,value.width,value.height])assert.ok(Number.isFinite(fraction)&&fraction>=0&&fraction<=1,`fraction in 0..1: ${fraction}`);
      assert.ok(value.x+value.width<=1+1e-9&&value.y+value.height<=1+1e-9,'the rectangle cannot pass the page edge');
      assert.ok(Object.isFrozen(value),'the published observation is immutable');
    }
});

test('a page narrower than the box shows its whole width and only part of its height',()=>{
  // Fit is height (1000x700 box, 500x1000 page), so a 1.5 scale is beyond it.
  const value=fixedPageViewport({page:0,pageWidth:500,pageHeight:1000,scale:1.5,fittedScale:.7,scroll:{left:0,top:250,width:1000,height:700},viewport});
  assert.ok(value);
  assert.equal(value.x,0);assert.equal(value.width,1);
  assert.equal(value.y,250/1500);assert.equal(value.height,700/1500);
});

test('page replacement reports the new page and its own dimensions',()=>{
  const next=fixedPageViewport({page:41,pageWidth:1224,pageHeight:1584,scale:1.25,fittedScale:.61,scroll:{left:120,top:60,width:viewport.width,height:viewport.height},viewport});
  assert.ok(next);
  assert.equal(next.page,41);assert.equal(next.pageWidth,1224);assert.equal(next.pageHeight,1584);assert.equal(next.scale,1.25);
  assert.equal(next.x,120/1530);assert.equal(next.y,60/1980);
});

test('an unusable box or page publishes nothing instead of a guessed rectangle',()=>{
  assert.equal(fixedPageViewport({...page,page:0,scale:2,fittedScale:1,scroll:{left:0,top:0,width:0,height:0},viewport:{width:0,height:0}}),undefined);
  assert.equal(fixedPageViewport({...page,page:0,scale:Number.NaN,fittedScale:1,scroll:{left:0,top:0,width:800,height:600},viewport}),undefined);
  assert.equal(fixedPageViewport({...page,page:-1,scale:2,fittedScale:1,scroll:{left:0,top:0,width:800,height:600},viewport}),undefined);
  assert.equal(fixedPageViewport({pageWidth:0,pageHeight:800,page:0,scale:2,fittedScale:1,scroll:{left:0,top:0,width:800,height:600},viewport}),undefined);
});
