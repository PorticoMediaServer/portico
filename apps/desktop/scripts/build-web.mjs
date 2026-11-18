// Builds the current web app into apps/desktop/dist, where Tauri takes it from.
import {execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';

const web = fileURLToPath(new URL('../../../web', import.meta.url));
const dist = fileURLToPath(new URL('../dist', import.meta.url));
execFileSync(process.execPath, [fileURLToPath(new URL('../../../node_modules/vite/bin/vite.js', import.meta.url)), 'build', '--outDir', dist, '--emptyOutDir'],
  {cwd: web, stdio: 'inherit'});
// The page's one inline stylesheet carries the placeholder a server fills with a fresh nonce.
// Tauri skips a style that already has a nonce, so the placeholder becomes Tauri's own token,
// which Tauri fills with a fresh nonce on every start and allows in the page's policy.
const page = `${dist}/index.html`;
writeFileSync(page, readFileSync(page, 'utf8').replaceAll('__PORTICO_STYLE_NONCE__', '__TAURI_STYLE_NONCE__'));
