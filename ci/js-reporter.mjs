// Node's parent-process file completion includes imports, child failures and
// process exit. Inner test events have entryFile and must not count as files.
import {relative, resolve} from 'node:path';

export default async function* fileReceipts(source) {
  const root = process.env.PORTICO_CI_ROOT;
  if (!root) throw new Error('PORTICO_CI_ROOT is required');
  for await (const {type, data} of source) {
    if (type === 'test:complete' && data.nesting === 0 && !data.entryFile && data.file && resolve(data.name) === resolve(data.file)) {
      yield JSON.stringify({file: relative(root, data.file).replaceAll('\\', '/'),
        status: data.details.passed === true && !data.skip ? 'pass' : 'fail',
        seconds: data.details.duration_ms / 1000}) + '\n';
    }
  }
}
