import {test} from 'node:test';
import {execFileSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';

test('a consumer including only its own entry can typecheck the public reader graph',()=>{
  const manifest=createRequire(import.meta.url).resolve('typescript/package.json');
  const compiler=fileURLToPath(new URL('./bin/tsc',pathToFileURL(manifest)));
  execFileSync(process.execPath,[compiler,'-p',fileURLToPath(new URL('./consumer/tsconfig.json',import.meta.url))],{
    // The canonical confirmed-snapshot projection carries client-core's public
    // graph too. Slow overflow runners still compile the same complete graph.
    encoding:'utf8',timeout:120000,env:{...process.env,NODE_OPTIONS:'--max-old-space-size=512',GOMAXPROCS:'1',GOMEMLIMIT:'512MiB'}
  });
});
