// Incremental project lifetime and per-file result reuse for the existing
// counters. Recognition/counting stays in their shared one-shot scan bodies.
import {API} from 'typescript/unstable/sync';
import {createVirtualFileSystem} from 'typescript/unstable/fs';
import {SyntaxKind as K} from 'typescript/unstable/ast';
import {isBuiltin} from 'node:module';
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
const excludedExecutable=path=>/\.(?:[cm]?ts|tsx|[cm]?js|jsx)$/.test(path)&&!/\.d\.[cm]?ts$/.test(path)&&
  /(?:^|\/)(?:tests?|__tests__|scripts|build|dist|Pods|dev)\/|\.(?:test|spec|browser|capture)\.|\/(?:ds-[^/]+|.*\.specimen)\.tsx$/.test(path);

export class TypedRatchet {
  constructor(root,kind,dependencyRoot=root) {
    if (!['census','query'].includes(kind))throw Error('Unknown typed ratchet scope');
    this.root=resolve(root);this.dependencyRoot=resolve(dependencyRoot);this.kind=kind;this.files=new Map();this.cache=new Map();
    this.thirdParty=new Map();this.directoryCache=new Map();this.configReads=new Set();this.graphs=new Map();this.sourceMetadata=new Map();
    this.interest=new Set();this.pending=new Set();this.invariant=new Set();
    this.verifiedPackages=new Set();
    this.requireMetadata=new Map();
    this.requireRoots=new Set();
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
  directRequires(file,path,oid) {
    const key=path+'\0'+oid;
    if(!this.requireMetadata.has(key)) {
      const requests=[];
      function visit(node) {
        if(node.kind===K.CallExpression) {
          let callee=node.expression;
          while([K.ParenthesizedExpression,K.AsExpression,K.TypeAssertionExpression,K.NonNullExpression,K.SatisfiesExpression].includes(callee.kind))callee=callee.expression;
          if(callee.kind===K.Identifier&&callee.text==='require') {
            const arg=node.arguments?.[0];
            requests.push(node.arguments?.length===1&&[K.StringLiteral,K.NoSubstitutionTemplateLiteral].includes(arg?.kind)?arg.text:null);
          }
        }
        node.forEachChild(visit);
      }
      visit(file);this.requireMetadata.set(key,requests);
    }
    return this.requireMetadata.get(key);
  }
  requireDependencies(path,requests,resolvedImports) {
    const dependencies=new Set(),errors=[];
    for(const specifier of requests) {
      if(specifier===null){errors.push('dynamic or unsupported require argument');continue;}
      if(isBuiltin(specifier))continue;
      if(!specifier.startsWith('.')) {
        if(!resolvedImports.has(specifier))errors.push(specifier);
        continue;
      }
      const target=relative(this.root,resolve(this.root,dirname(path),specifier));
      if(target.startsWith('..'+sep)||target==='..'){errors.push(specifier);continue;}
      if(target.endsWith('.json')) {
        if(this.files.has(target)){dependencies.add(target);this.interest.add(resolve(this.root,target));}
        else errors.push(specifier);
        continue;
      }
      // Metro's existing bundled PNG loads are data, not executable modules.
      // This does not spare any other call in the importing source file.
      if(/^(?:apps\/native|apps\/vega)\//.test(path)&&target.endsWith('.png'))continue;
      const suffixes=['.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs'];
      const candidates=this.files.has(target)&&/\.(?:[cm]?ts|tsx|[cm]?js|jsx)$/.test(target)?[target]:
        suffixes.flatMap(suffix=>[target+suffix,target+'/index'+suffix]).filter(p=>this.files.has(p));
      // A package entry/condition cannot be guessed from an index filename.
      if(!candidates.length||(!this.files.has(target)&&this.files.has(target+'/package.json')))errors.push(specifier);
      else for(const candidate of candidates)dependencies.add(candidate);
    }
    return {dependencies,errors};
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
  verifyInstalledPackage(path) {
    // Read declarations only from a package selected by the install's own
    // lockfile. Workspace aliases have already been mapped to exact blobs.
    const split=path.indexOf(sep+'node_modules'+sep);
    if(split<0)return;
    const install=path.slice(0,split),suffix=path.slice(split+1).split(sep);
    const at=suffix.lastIndexOf('node_modules'),length=suffix[at+1]?.startsWith('@')?3:2;
    if(suffix.length<at+length)return;
    const key=suffix.slice(0,at+length).join('/'),packagePath=resolve(install,key);
    if(this.verifiedPackages.has(packagePath))return;
    const lock=JSON.parse(readFileSync(resolve(install,'package-lock.json'),'utf8'));
    const expected=lock.packages?.[key];
    const actual=JSON.parse(readFileSync(resolve(packagePath,'package.json'),'utf8'));
    // npm/semver ignores build metadata (Kepler publishes 4.0.1 with a
    // 4.0.1+rn... manifest). Prerelease and selected versions still must match.
    if(!expected || expected.link || !expected.version ||
      String(actual.version).split('+')[0]!==expected.version.split('+')[0])
      throw Error('Installed dependency does not match verified lockfile: '+packagePath);
    this.verifiedPackages.add(packagePath);
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
        if(!this.thirdParty.has(path)) {
          // Missing candidate files are normal resolution misses. A present
          // declaration without a verifiable install is a policy failure;
          // never swallow its ENOENT as though the declaration were absent.
          const contents=readFileSync(path,'utf8');
          try {this.verifyInstalledPackage(path);}
          catch(error) {throw Error('Installed dependency provenance failure: '+error.message);}
          this.thirdParty.set(path,contents);
        }
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
  advance(input,expansion=0) {
    if(expansion>32)throw Error('CommonJS closure expansion exceeded its bound; no result accepted');
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
    const fallback=resolve(this.root,'.portico-ratchet/query.json');
    const required=[...this.requireRoots].filter(p=>this.files.has(p));
    if(this.kind==='census') {
      virtual[this.config]=JSON.stringify(censusProjectConfig(this.root,[...new Set([...(input.projectFiles??input.files),...required])]));
      if(this.previousConfig!==virtual[this.config])changed.push(this.config);
      this.previousConfig=virtual[this.config];
    } else if(required.length||input.files.some(p=>! /\.(?:ts|tsx)$/.test(p))) {
      // App configs need not include a newly supported executable extension.
      // Keep their resolution first and give every remaining counted source
      // a project, rather than accepting an unanalysed zero.
      virtual[fallback]=JSON.stringify(censusProjectConfig(this.root,[...new Set([...input.files.filter(p=>! /\.(?:ts|tsx)$/.test(p)),...required])]));
      if(this.previousConfig!==virtual[fallback])changed.push(fallback);
      this.previousConfig=virtual[fallback];configs.push(fallback);
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
    // A failed snapshot must never inherit the previous successful answer on
    // an identical retry (including the syntactic-zero fast path).
    this.lastAnswer=null;
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
    const wanted=new Set(input.files),selected=new Map(),contexts=new Map(),syntax=[];
    for(const config of configs)if(!this.snapshot.getProject(config))throw Error('Missing compiler project: '+config);
    // Preserve the existing counter's project ordering; the extension-only
    // fallback is last and cannot replace an app's resolved type context.
    const projects=[...this.snapshot.getProjects()].sort((a,b)=>(a.configFileName===fallback)-(b.configFileName===fallback));
    for(const project of projects) {
      const filenames=[...project.program.getSourceFileNames()].sort();
      const diagnostics=[...project.program.getConfigFileParsingDiagnostics()];
      syntax.push(...project.program.getSyntacticDiagnostics());
      if(diagnostics.length)throw Error('Ratchet parse/config diagnostic: '+diagnostics.slice(0,8)
        .map(d=>`${d.fileName??project.configFileName}: ${d.code} ${d.text}`).join('; '));
      for(const filename of filenames) {
        const path=relative(this.root,filename);
        if(wanted.has(path)&&!selected.has(path))selected.set(path,project);
      }
      if(![...selected.values()].includes(project)&&!(project.configFileName===fallback&&required.length))continue;
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
        const resolvedImports=new Set(imports.filter((_,i)=>symbols[i]?.declarations?.length).map(n=>n.text));
        const commonJS=this.requireDependencies(path,this.directRequires(file,path,meta.oid),resolvedImports);
        for(const dependency of commonJS.dependencies)dependencies.add(dependency);
        const excludedImport=imports.find(imported=>(imported.text?.startsWith('.')||imported.text?.startsWith('/'))&&
          (excludedExecutable(imported.text) || /(?:^|\/)(?:tests?|__tests__|scripts|build|dist|Pods|dev)\//.test(imported.text)))?.text;
        let known=!(file.referencedFiles?.length || file.typeReferenceDirectives?.length || file.libReferenceDirectives?.length);
        for(const symbol of symbols) {
          if(!symbol?.declarations?.length){known=false;continue;}
          for(const declaration of symbol.declarations) {
            const dependency=relative(this.root,declaration.path);
            if(this.files.has(dependency))dependencies.add(dependency);
            else if(!declaration.path.split(sep).includes('node_modules'))known=false;
          }
        }
        const edges={dependencies,known,excludedImport,requireErrors:commonJS.errors,requireTargets:[...commonJS.dependencies].filter(p=>!p.endsWith('.json'))};graph.set(path,edges);
        next.set(path,{oid:meta.oid,global:meta.global,resolution,edges});this.stats.dependencyAnalyses++;
      }
      for(const dependency of new Set([...graph.values()].flatMap(edges=>[...edges.dependencies])))
        if(dependency.endsWith('.json')&&!graph.has(dependency))graph.set(dependency,{dependencies:new Set(),known:true,json:true});
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
    // Inventory the whole production-reachable executable closure, including
    // excluded directories reached through barrels or dynamic imports. A
    // label cannot hide debt; declaration-only dependencies remain legitimate.
    const scanFiles=[...input.files],reachable=new Set(scanFiles),needed=new Set(),owners=new Map(),ordinary=new Set(),requireFailures=[];
    for(const [project,{graph}]of contexts)for(const at of graph.keys()) {
      if(!owners.has(at))owners.set(at,project);
      if(project.configFileName!==fallback)ordinary.add(at);
    }
    for(const path of input.files) {
      const project=selected.get(path);if(!project)throw Error('No compiler project for '+path);
      const {graph}=contexts.get(project),seen=new Set(),pending=[path];
      while(pending.length) {
        const at=pending.pop();if(seen.has(at))continue;seen.add(at);
        const owner=graph.has(at)?project:owners.get(at);
        const edges=contexts.get(owner)?.graph.get(at);
        if(edges?.requireErrors?.length)requireFailures.push(at+' -> '+edges.requireErrors.join(', '));
        if(edges?.json) {
          try {JSON.parse(this.files.get(at).text.replace(/^\uFEFF/,''));}
          catch {throw Error('Invalid CommonJS JSON input: '+at);}
        }
        for(const target of edges?.requireTargets??[])if(this.kind==='census'
          ? !(input.projectFiles??input.files).includes(target)
          : !ordinary.has(target)&&!wanted.has(target))needed.add(target);
        if(owner&&excludedExecutable(at)&&!reachable.has(at)) {
          reachable.add(at);scanFiles.push(at);selected.set(at,owner);
        }
        if(edges?.excludedImport&&!edges.dependencies.size)
          throw Error('Unresolved production import into excluded executable area: '+at+' -> '+edges.excludedImport);
        pending.push(...(edges?.dependencies??[]));
      }
    }
    if(needed.size!==this.requireRoots.size||[...needed].some(p=>!this.requireRoots.has(p))) {
      this.requireRoots=needed;return this.advance(input,expansion+1);
    }
    if(syntax.length)throw Error('Ratchet parse/config diagnostic: '+syntax.slice(0,8).map(d=>`${d.fileName}: ${d.code} ${d.text}`).join('; '));
    if(requireFailures.length)throw Error('Unverified CommonJS executable closure: '+requireFailures.join('; ')+'; no result accepted');
    if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'dependencies',milliseconds:performance.now()-began})+'\n');
    const results={},missing=[],signatures=new Map();
    // Newly inventoried files can be present in more than one app project.
    // Bind their results conservatively to the full resolution environment,
    // rather than borrowing the importing root's narrower project signature.
    const completeEnvironment=hash([configs,[...this.files].map(([p,v])=>[p,v.oid])]);
    const extraSignature=scanFiles.length>input.files.length?completeEnvironment:null;
    for(const path of scanFiles) {
      if(this.kind==='query'&&this.invariant.has(path+'\0'+this.files.get(path)?.oid)) {
        results[path]=[];this.stats.reusedFiles++;continue;
      }
      const project=selected.get(path);if(!project)throw Error('No compiler project for '+path);
      const {graph,context,whole}=contexts.get(project),visited=new Set(),pending=[path],foreign=new Set();let known=true;
      while(pending.length) {
        const at=pending.pop();if(visited.has(at))continue;visited.add(at);
        const owner=graph.has(at)?project:owners.get(at),scope=contexts.get(owner);
        const edges=scope?.graph.get(at);if(!edges){known=false;continue;}
        if(owner!==project)foreign.add(scope.context+'\0'+scope.whole);
        if(!edges.known)known=false;
        pending.push(...edges.dependencies);
      }
      // Unresolved/ambient provenance gets a whole-project signature. It may
      // reuse only when every possible loaded binding and resolution input is
      // identical; it never gets an optimistic per-file dependency key.
      const signature=!wanted.has(path)?extraSignature:hash([context,known?null:whole,[...foreign].sort(),[...visited].sort().map(p=>[p,this.files.get(p)?.oid??'missing'])]);
      const cached=this.cache.get(path+'\0'+signature);
      if(cached!==undefined){results[path]=cached;this.stats.reusedFiles++;}
      else {missing.push(path);signatures.set(path,signature);}
    }
    if(this.kind==='census') {
      const fresh=[];
      for(const project of new Set(missing.map(p=>selected.get(p))))fresh.push(...analyseCensus({root:this.root,files:missing.filter(p=>selected.get(p)===project)},{project,readFile:p=>{
        const value=this.files.get(relative(this.root,p));if(!value)throw Error('Missing exact source '+p);return value.text;
      }}));
      for(const path of missing)results[path]=fresh.filter(site=>site.file===path);
    } else {
      const fresh=analyseQueryReads({root:this.root,configs:input.configs,files:missing},{snapshot:this.snapshot,projects});
      for(const path of fresh.invariant)this.invariant.add(path+'\0'+this.files.get(path).oid);
      for(const path of missing)results[path]=fresh.reads[path]??[];
    }
    for(const path of missing) {
      this.stats.analysedFiles++;
      if(signatures.get(path))this.cache.set(path+'\0'+signatures.get(path),results[path]);
    }
    this.stats.lastMilliseconds=performance.now()-began;
    if(process.env.PORTICO_RATCHET_TRACE)process.stderr.write(JSON.stringify({kind:this.kind,stage:'result',...this.stats})+'\n');
    this.lastAnswer=this.kind==='census'?{sites:scanFiles.flatMap(p=>results[p]),inventory:scanFiles}:
      {reads:Object.fromEntries(Object.entries(results).filter(([,lines])=>lines.length)),inventory:scanFiles};
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
