import {build} from 'vite';
import {fileURLToPath} from 'node:url';
import {isAbsolute, relative, resolve} from 'node:path';

// Run inside the approved heavy wrapper. Only disposable caller-owned TMPDIR
// receives output; no application root, API fixture or checked source is reset.
if (!process.env.TMPDIR || !isAbsolute(process.env.TMPDIR)) throw new Error('An absolute owned TMPDIR is required.');
const scratch = resolve(process.env.TMPDIR), outDir = resolve(scratch, 'reader-display-build');
if (relative(scratch, outDir) !== 'reader-display-build') throw new Error('Display output must remain in owned scratch.');
const root = fileURLToPath(new URL('../../', import.meta.url));
await build({root, configFile: fileURLToPath(new URL('../../vite.config.ts', import.meta.url)),
  build: {outDir, emptyOutDir: true, rolldownOptions: {input: fileURLToPath(new URL('./display.html', import.meta.url))}}});
