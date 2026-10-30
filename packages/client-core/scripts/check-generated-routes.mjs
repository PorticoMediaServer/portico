import {readFileSync, readdirSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {join, relative} from 'node:path';

/** Find code string literals, ignoring comments; templates retain their expressions. */
export function pathLiterals(source) {
 const paths=[];
 for(let i=0;i<source.length;i++) {
  if(source[i]==='/'&&source[i+1]==='/'){i=source.indexOf('\n',i+2);if(i<0)break;continue;}
  if(source[i]==='/'&&source[i+1]==='*'){const end=source.indexOf('*/',i+2);if(end<0)break;i=end+1;continue;}
  const quote=source[i];if(!['"',"'",'`'].includes(quote))continue;
  const start=i++;let value='';
  for(;i<source.length;i++){if(source[i]==='\\'){value+=source[i]+(source[++i]??'');continue;}if(source[i]===quote)break;value+=source[i];}
  if(value.includes('/v1/'))paths.push({value,line:source.slice(0,start).split('\n').length});
 }
 return paths;
}

export function checkGeneratedRoutes(root=fileURLToPath(new URL('../src/',import.meta.url))) {
 const errors=[];
 function visit(dir) {for(const entry of readdirSync(dir,{withFileTypes:true})){
  const file=join(dir,entry.name);if(entry.isDirectory()){visit(file);continue;}if(!entry.name.endsWith('.ts'))continue;
  const name=relative(root,file).replaceAll('\\','/');
  for(const {value,line}of pathLiterals(readFileSync(file,'utf8'))){errors.push(`${name}:${line}: use a generated API route builder`);}
 }}
 visit(root);return errors;
}
if(process.argv[1]===fileURLToPath(import.meta.url)){
 const errors=checkGeneratedRoutes();if(errors.length){process.stderr.write(errors.join('\n')+'\n');process.exitCode=1;}
 else process.stdout.write('Generated-route check passed; every client API path uses a registered contract.\n');
}
