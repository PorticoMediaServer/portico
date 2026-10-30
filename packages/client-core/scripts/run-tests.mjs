import {readdirSync} from 'node:fs';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';

function tests(directory) {
  return readdirSync(directory, {withFileTypes: true}).sort((a, b) => a.name.localeCompare(b.name)).flatMap(entry => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? tests(path) : /\.test\.(?:tsx?|[cm]?js)$/.test(entry.name) ? [path] : [];
  });
}

// The Go-produced Reading Home vectors exist only where CI records them (see test/final-batch/README.md).
const fixtures = !!process.env.PORTICO_READING_HOME_FIXTURE_DIR;
const files = tests('test').filter(file => fixtures || !file.startsWith(join('test', 'final-batch') + '/'));
if (!fixtures) console.log('final-batch: not run (PORTICO_READING_HOME_FIXTURE_DIR is unset; see test/final-batch/README.md)');
// The same transform mode CI's runner uses: some sources use constructor parameter properties.
const result = spawnSync(process.execPath, ['--experimental-transform-types', '--test', ...files], {stdio: 'inherit'});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
