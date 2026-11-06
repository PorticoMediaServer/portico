// Incremental project lifetime and per-file result reuse for the existing
// counters. Recognition/counting stays in their shared one-shot scan bodies.
import {API} from 'typescript/unstable/sync';
import {createVirtualFileSystem} from 'typescript/unstable/fs';
import {readFileSync, existsSync, statSync, readdirSync, realpathSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {resolve, relative, dirname, sep} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath} from 'node:url';
import {analyseCensus,censusProjectConfig} from './client_api_sites.mjs';
import {analyseQueryReads} from './query_reads.mjs';

const hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
const sourceSuffix=/\.(?:[cm]?ts|tsx|[cm]?js|jsx|json)$/;
const below=(root,path)=>path===root || path.startsWith(root+sep);

export class TypedRatchet {
  constructor(root,kind,dependencyRoot=root) {
    if (!['census','query'].includes(kind))throw Error('Unknown typed ratchet scope');
    this.root=resolve(root);this.dependencyRoot=resolve(dependencyRoot);this.kind=kind;this.files=new Map();this.cache=new Map();
    this.thirdParty=new Map();this.directoryCache=new Map();this.configReads=new Set();this.graphs=new Map();this.sourceMetadata=new Map();
    this.interest=new Set();this.pending=new Set();this.invariant=new Set();
    this.stats={analysedFiles:0,reusedFiles:0,snapshots:0,projectOpens:0,dependencyAnalyses:0,dependencyReuses:0,unchangedPrograms:0};
    this.config=resolve(this.root,'.portico-ratchet/census.json');
    this.host={};
    for(const name of ['fileExists','directoryExists','getAccessibleEntries','readFile','realpath'])
      this.host[name]=path=>this.filesystem(name,resolve(path));
  }
  packageAliases() {
    const rootManifest=this.files.get('package.json');
    if(!rootManifest)return new Map();
    this.interest.add(resolve(this.root,'package.json'));
    const manifest=JSON.parse(rootManifest.text),patterns=manifest.workspaces?.packages??manifest.workspaces??[];
    if(!Array.isArray(patterns))throw Error('Unknown workspace configuration');
    const matches=patterns.map(pattern=>{
      if(typeof pattern!=='string' || /[{}!\[\]]/.test(pattern))throw Error('Unsupported workspace pattern');
      return new RegExp('^'+pattern.split('/').map(part=>part==='**'?'.*':part.split('*').map(s=>s.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('[^/]*')).join('/')+'$');
    });
    const aliases=new Map();
    for(const [path,file] of this.files) {
      if(!path.endsWith('/package.json') || !matches.some(p=>p.test(dirname(path))))continue;
      this.interest.add(resolve(this.root,path));
      const name=JSON.parse(file.text).name;
      if(!/^(?:@[A-Za-z0-9._-]+\/)?[A-Za-z0-9._-]+$/.test(name))throw Error('Invalid historical workspace name');
      if(aliases.has(name))throw Error('Duplicate historical workspace name');
      aliases.set(name,resolve(this.root,dirname(path)));
    }
    return aliases;
  }
  workspacePath(path) {
    const prefix=resolve(this.root,'node_modules')+sep;
    if(!path.startsWith(prefix))return undefined;
    const parts=path.slice(prefix.length).split(sep),name=parts[0].startsWith('@')?parts.slice(0,2).join('/'):parts[0];
    const target=this.aliases.get(name);
    if(target)return resolve(target,...parts.slice(name.startsWith('@')?2:1));
    // Existing npm workspace links are never a route back to current sources.
    const packagePath=resolve(prefix,name);
    try {if(!realpathSync(this.dependencyPath(packagePath)).split(sep).includes('node_modules'))return null;} catch {}
    return undefined;
  }
  dependencyPath(path) {
    return below(this.root,path)&&relative(this.root,path).split(sep).includes('node_modules')
      ?resolve(this.dependencyRoot,relative(this.root,path)):path;
  }
  workspaceChildren(path) {
    const modules=resolve(this.root,'node_modules');
    if(path===modules)return [...new Set([...this.aliases.keys()].map(name=>name.split('/')[0]))];
    if(dirname(path)===modules&&path.split(sep).at(-1).startsWith('@'))
      return [...this.aliases.keys()].filter(name=>name.startsWith(path.split(sep).at(-1)+'/')).map(name=>name.split('/')[1]);
    return [];
  }
  filesystem(method,path) {
    this.interest.add(path);
    const alias=this.workspacePath(path);
    if(alias===null)return method==='readFile'?null:method==='getAccessibleEntries'?{files:[],directories:[]}:method==='realpath'?path:false;
    if(alias!==undefined)path=alias;
    this.interest.add(path);
    const local=below(this.root,path) && !relative(this.root,path).split(sep).includes('node_modules');
    if(local) {
      if(method==='readFile') {
        const key=relative(this.root,path),file=this.files.get(key);
        if(file && key.endsWith('.json'))this.configReads.add(key);
        return this.vfs.readFile(path)??null;
      }
      if(method==='getAccessibleEntries') {
        const entries=this.vfs.getAccessibleEntries(path)??{files:[],directories:[]};
        if(path===this.root && (this.aliases.size||existsSync(resolve(this.dependencyRoot,'node_modules'))))
          return {files:entries.files,directories:[...new Set([...entries.directories,'node_modules'])]};
        return entries;
      }
      return this.vfs[method](path);
    }
    // Immutable third-party declarations come from this invocation's real
    // install. No dependency/source directory or symlink is ever created.
    if(!path.split(sep).includes('node_modules')) {
      if(method==='readFile')return null;
      if(method==='getAccessibleEntries')return {files:[],directories:[]};
      return method==='realpath'?path:existsSync(path)&&statSync(path).isDirectory();
    }
    const logical=path;
    const virtualChildren=this.workspaceChildren(logical);
    path=this.dependencyPath(path);
    const cacheKey=method+'\0'+path;
    if(this.directoryCache.has(cacheKey))return this.directoryCache.get(cacheKey);
    let answer;
    try {
      if(method==='readFile') {
        if(!this.thirdParty.has(path))this.thirdParty.set(path,readFileSync(path,'utf8'));
        answer=this.thirdParty.get(path);
      } else if(method==='fileExists')answer=statSync(path).isFile();
      else if(method==='directoryExists')answer=statSync(path).isDirectory();
      else if(method==='realpath')answer=realpathSync(path);
      else {
        const entries=readdirSync(path,{withFileTypes:true});
        const files=[],directories=[];
        for(const entry of entries) {
          const at=resolve(logical,entry.name),mapped=this.workspacePath(at);
          if(mapped===null)continue;
          (mapped!==undefined || entry.isDirectory() || entry.isSymbolicLink()&&statSync(this.dependencyPath(at)).isDirectory()?directories:files).push(entry.name);
        }
        answer={files:[...new Set(files)],directories:[...new Set(directories)]};
      }
    } catch(error) {
      if(!['ENOENT','ENOTDIR'].includes(error.code))throw error;
      answer=method==='readFile'?null:method==='getAccessibleEntries'?{files:[],directories:[]}:method==='realpath'?path:false;
    }
    if(virtualChildren.length) {
      if(method==='getAccessibleEntries')answer={files:answer.files,directories:[...new Set([...answer.directories,...virtualChildren])]};
      if(method==='directoryExists')answer=true;
    }
    // Alias enumeration changes with each tree; all actual dependency reads are
    // invocation-fixed. The directory cache is cleared when aliases change.
    this.directoryCache.set(cacheKey,answer);return answer;
  }
  advance(input) {
    const began=performance.now();
    const next=new Map(Object.entries(input.sources));
    for(const [path,value] of next) {
      if(path.startsWith('.portico-ratchet/') || resolve(this.root,path)!==resolve(this.root)+sep+path || !sourceSuffix.test(path)
          || typeof value.text!=='string' || typeof value.oid!=='string')throw Error('Invalid immutable typed source record');
    }
    const changed=[],created=[],deleted=[];
    for(const [path,value] of next)(this.files.has(path)?this.files.get(path).oid===value.oid?[]:changed:created).push(resolve(this.root,path));
    for(const path of this.files.keys())if(!next.has(path))deleted.push(resolve(this.root,path));
    this.files=next;this.aliases=this.packageAliases();this.directoryCache.clear();
    const virtual={};for(const [path,value] of next)virtual[resolve(this.root,path)]=value.text;
    const configs=this.kind==='census'?[this.config]:input.configs.map(p=>resolve(this.root,p));
    if(this.kind==='census') {
      virtual[this.config]=JSON.stringify(censusProjectConfig(this.root,input.projectFiles??input.files));
      if(this.previousConfig!==virtual[this.config])changed.push(this.config);
      this.previousConfig=virtual[this.config];
    }
    this.vfs=createVirtualFileSystem(virtual);
    const configList=JSON.stringify(configs);
    const selection=JSON.stringify([input.files,input.projectFiles??input.files,configs]);
    const unchanged=this.lastAnswer&&selection===this.selection&&!created.length&&!deleted.length&&!changed.some(p=>this.interest.has(p));
    for(const p of changed)this.pending.add(p);
    if(unchanged) {
      this.stats.unchangedPrograms++;this.stats.reusedFiles+=input.files.length;
      this.stats.lastMilliseconds=performance.now()-began;
      return {...this.lastAnswer,stats:{...this.stats}};
    }
    if(this.configList && this.configList!==configList){this.close();this.configReads.clear();}
    if(!this.api) {
      this.api=new API({cwd:this.root,fs:this.host});
      this.snapshot=this.api.updateSnapshot({openProjects:configs});
      this.stats.projectOpens+=configs.length;
    } else {
      const previous=this.snapshot;
      this.snapshot=this.api.updateSnapshot({fileChanges:{changed:[...this.pending].filter(p=>!created.includes(p)&&!deleted.includes(p)),created,deleted}});previous.dispose();
    }
    this.pending.clear();this.selection=selection;
    this.configList=configList;this.stats.snapshots++;
    if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'snapshot',milliseconds:performance.now()-began})+'\n');
    const wanted=new Set(input.files),selected=new Map(),contexts=new Map();
    for(const project of this.snapshot.getProjects()) {
      const filenames=[...project.program.getSourceFileNames()].sort();
      for(const filename of filenames) {
        const path=relative(this.root,filename);
        if(wanted.has(path)&&!selected.has(path))selected.set(path,project);
      }
      if(![...selected.values()].includes(project))continue;
      const graph=new Map(),globals=[],externals=[],metadata=new Map();
      const metadataContext=hash([project.configFileName,project.compilerOptions]);
      // Source metadata/edges are plain values, never handles from an old
      // compiler snapshot. Resolution changes invalidate every cached edge;
      // ordinary module edits invalidate only that module and its dependants.
      for(const filename of filenames) {
        const path=relative(this.root,filename),record=this.files.get(path);
        if(!record) {
          if(!filename.split(sep).includes('node_modules'))throw Error('Unverified source outside exact tree: '+filename);
          externals.push(filename);continue;
        }
        const metadataKey=metadataContext+'\0'+path+'\0'+record.oid;
        const cached=this.sourceMetadata.get(metadataKey);
        const file=cached?null:project.program.getSourceFile(filename);
        if(!cached&&!file)throw Error('Missing compiler dependency: '+path);
        const global=file?(!file.externalModuleIndicator||!!file.moduleAugmentations?.length):cached.global;
        this.sourceMetadata.set(metadataKey,{global});
        metadata.set(path,{oid:record.oid,global,file,filename});
        if(global)globals.push([path,record.oid]);
      }
      const resolutionParts=[filenames,[...this.files.keys()].sort(),globals.sort(),project.compilerOptions,
        [...this.files].filter(([p])=>p.endsWith('.json')&&(this.configReads.has(p)||/(?:^|\/)(?:package|tsconfig[^/]*)\.json$/.test(p))).map(([p,v])=>[p,v.oid]).sort()];
      const resolution=hash(resolutionParts);
      if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'resolution',config:project.configFileName,parts:resolutionParts.map(hash)})+'\n');
      const next=new Map();
      const previous=this.graphs.get(project.configFileName+'\0'+resolution)??new Map();
      for(const [path,meta]of metadata) {
        const cached=previous.get(path);
        if(cached?.oid===meta.oid&&cached.resolution===resolution) {
          graph.set(path,cached.edges);next.set(path,cached);this.stats.dependencyReuses++;continue;
        }
        const file=meta.file??project.program.getSourceFile(meta.filename);
        if(!file)throw Error('Missing compiler dependency: '+path);
        const imports=file.imports??[],symbols=project.checker.getSymbolAtLocation(imports),dependencies=new Set();
        let known=!(file.referencedFiles?.length || file.typeReferenceDirectives?.length || file.libReferenceDirectives?.length);
        for(const symbol of symbols) {
          if(!symbol?.declarations?.length){known=false;continue;}
          for(const declaration of symbol.declarations) {
            const dependency=relative(this.root,declaration.path);
            if(this.files.has(dependency))dependencies.add(dependency);
            else if(!declaration.path.split(sep).includes('node_modules'))known=false;
          }
        }
        const edges={dependencies,known};graph.set(path,edges);
        next.set(path,{oid:meta.oid,global:meta.global,resolution,edges});this.stats.dependencyAnalyses++;
      }
      this.graphs.set(project.configFileName+'\0'+resolution,next);
      const options=project.compilerOptions;
      // Root selection, global/ambient files, resolution configs and loaded
      // external libraries all participate; unknown provenance is never reused.
      const configContext=[...this.configReads].sort().map(p=>[p,this.files.get(p)?.oid??'missing']);
      for(const [p,value] of this.files)if(p.endsWith('package-lock.json'))configContext.push([p,value.oid]);
      // An added external module is not a global input. Its effect reaches a
      // known caller through resolved edges; ambient/script additions are in
      // globals. Unknown provenance still binds the complete project below.
      const context=hash([project.configFileName,options,globals.sort(),externals.sort(),configContext]);
      const whole=hash([resolution,filenames.map(p=>[p,this.files.get(relative(this.root,p))?.oid??'installed'])]);
      contexts.set(project,{graph,context,whole});
    }
    if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'dependencies',milliseconds:performance.now()-began})+'\n');
    const results={},missing=[],signatures=new Map();
    for(const path of input.files) {
      if(this.kind==='query'&&this.invariant.has(path+'\0'+this.files.get(path)?.oid)) {
        results[path]=[];this.stats.reusedFiles++;continue;
      }
      const project=selected.get(path);if(!project)throw Error('No compiler project for '+path);
      const {graph,context,whole}=contexts.get(project),visited=new Set(),pending=[path];let known=true;
      while(pending.length) {
        const at=pending.pop();if(visited.has(at))continue;visited.add(at);
        const edges=graph.get(at);if(!edges){known=false;continue;}
        if(!edges.known)known=false;
        pending.push(...edges.dependencies);
      }
      // Unresolved/ambient provenance gets a whole-project signature. It may
      // reuse only when every possible loaded binding and resolution input is
      // identical; it never gets an optimistic per-file dependency key.
      const signature=hash([context,known?null:whole,[...visited].sort().map(p=>[p,this.files.get(p)?.oid??'missing'])]);
      const cached=this.cache.get(path+'\0'+signature);
      if(cached!==undefined){results[path]=cached;this.stats.reusedFiles++;}
      else {missing.push(path);signatures.set(path,signature);}
    }
    if(this.kind==='census') {
      const project=this.snapshot.getProject(this.config);
      const fresh=analyseCensus({root:this.root,files:missing},{project,readFile:p=>{
        const value=this.files.get(relative(this.root,p));if(!value)throw Error('Missing exact source '+p);return value.text;
      }});
      for(const path of missing)results[path]=fresh.filter(site=>site.file===path);
    } else {
      const fresh=analyseQueryReads({root:this.root,configs:input.configs,files:missing},{snapshot:this.snapshot});
      for(const path of fresh.invariant)this.invariant.add(path+'\0'+this.files.get(path).oid);
      for(const path of missing)results[path]=fresh.reads[path]??[];
    }
    for(const path of missing) {
      this.stats.analysedFiles++;
      if(signatures.get(path))this.cache.set(path+'\0'+signatures.get(path),results[path]);
    }
    this.stats.lastMilliseconds=performance.now()-began;
    if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'result',...this.stats})+'\n');
    this.lastAnswer=this.kind==='census'?{sites:input.files.flatMap(p=>results[p])}:
      {reads:Object.fromEntries(Object.entries(results).filter(([,lines])=>lines.length))};
    return {...this.lastAnswer,stats:{...this.stats}};
  }
  close(){if(this.api)this.api.close();this.api=null;this.snapshot=null;}
}

if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  let worker;
  try {
    for await(const line of createInterface({input:process.stdin,crlfDelay:Infinity})) {
      const input=JSON.parse(line);
      if(!worker)worker=new TypedRatchet(input.root,input.kind,input.dependencyRoot);
      if(worker.root!==resolve(input.root)||worker.kind!==input.kind||worker.dependencyRoot!==resolve(input.dependencyRoot??input.root))throw Error('Typed scope changed inside invocation');
      process.stdout.write(JSON.stringify({ok:true,...worker.advance(input)})+'\n');
    }
  } catch(error) {process.stdout.write(JSON.stringify({ok:false,error:String(error.stack??error)})+'\n');process.exitCode=1;}
  finally {worker?.close();}
}
