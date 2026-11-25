const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../view.js'),'utf8');

function element(id=''){
 const attrs=new Map(),classes=new Set(),children=[];
 const el={
  id,dataset:{},style:{setProperty(){}},hidden:false,textContent:'',className:'',
  firstElementChild:{setAttribute(k,v){attrs.set('child:'+k,v);},getAttribute:k=>attrs.get('child:'+k)??null},
  get src(){return attrs.get('src')||'';},
  set src(value){attrs.set('src',String(value));},
  getAttribute:k=>attrs.has(k)?attrs.get(k):null,
  setAttribute:(k,v)=>{attrs.set(k,String(v));if(k==='hidden')el.hidden=true;},
  removeAttribute:k=>{attrs.delete(k);if(k==='hidden')el.hidden=false;},
  hasAttribute:k=>attrs.has(k),
  classList:{toggle:(name,on)=>{if(on)classes.add(name);else classes.delete(name);},contains:name=>classes.has(name)},
  replaceChildren:()=>{children.length=0;el.textContent='';},
  appendChild:child=>{children.push(child);el.textContent+=child.textContent||'';return child;},
 };
 return el;
}

function createView(){
 const nodes=new Map();
 const get=id=>{if(!nodes.has(id))nodes.set(id,element(id));return nodes.get(id);};
 const document={
  getElementById:get,
  querySelectorAll:()=>[],
  createElement:tag=>element(tag),
  createTextNode:value=>({textContent:String(value)}),
 };
 const window={
  innerWidth:1920,innerHeight:1080,
  addEventListener(){},
  porticoCastText:{t:key=>key},
 };
 const sandbox={window,document,setTimeout:()=>1,clearTimeout(){},setInterval:()=>1,clearInterval(){}};
 vm.runInNewContext(source,sandbox);
 return {view:window.porticoCastView,node:get};
}

test('async artwork arriving while already in playing state updates playing art and ambient immediately',()=>{
 const {view,node}=createView();
 const meta={title:'Track One',artist:'Artist',albumTitle:'Album',kind:'song'};
 view.loading(meta);
 view.playing({meta,state:'playing',positionSeconds:5,durationSeconds:180});
 assert.equal(node('ui').dataset.state,'playing');
 assert.equal(node('playing-art').getAttribute('src'),null);
 assert.equal(node('playing-art').hidden,true);
 assert.equal(node('playing-art-tile').classList.contains('none'),true);

 const dataUrl='data:image/png;base64,AAAA';
 view.artwork(dataUrl,true);
 assert.equal(node('playing-art').getAttribute('src'),dataUrl);
 assert.equal(node('playing-ambient').getAttribute('src'),dataUrl);
 assert.equal(node('playing-art').hidden,false);
 assert.equal(node('playing-ambient').hidden,false);
 assert.equal(node('playing-art-tile').classList.contains('none'),false);
 assert.equal(node('playing-glyph').hasAttribute('hidden'),true);
 assert.equal(node('playing-shade').hasAttribute('hidden'),false);

 // Subsequent progress tick for the same item retains the resolved artwork.
 view.playing({meta,state:'playing',positionSeconds:15,durationSeconds:180});
 assert.equal(node('playing-art').getAttribute('src'),dataUrl);
 assert.equal(node('playing-art').hidden,false);
});

test('artwork arriving during loading state still populates loading art and carries into playing',()=>{
 const {view,node}=createView();
 const meta={title:'Movie',kind:'movie'};
 view.loading(meta);
 const dataUrl='data:image/webp;base64,BBBB';
 view.artwork(dataUrl,false);
 assert.equal(node('loading-art').getAttribute('src'),dataUrl);
 assert.equal(node('loading-art').hidden,false);
 assert.equal(node('playing-art').getAttribute('src'),null);

 view.playing({meta,state:'playing',positionSeconds:0,durationSeconds:3600});
 assert.equal(node('playing-art').getAttribute('src'),dataUrl);
 assert.equal(node('playing-art').hidden,false);
});
