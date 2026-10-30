/** Compatibility wrapper around the actual production input; no second entry selector. */
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {defineConfig} from 'vite';
import base from '../vite.config';

const scratch = process.env.TMPDIR;
if (!scratch || !path.isAbsolute(scratch)) throw new Error('TMPDIR must name the owned build scratch.');
export default defineConfig(async env => {
  const config = await (typeof base === 'function' ? base(env) : base);
  return {
  ...config,
  root: fileURLToPath(new URL('../', import.meta.url)),
  cacheDir: path.join(scratch, 'services-vite-cache'),
  build: {...config.build, outDir: path.join(scratch, 'services-dist'), emptyOutDir: true, manifest: true},
}; });
