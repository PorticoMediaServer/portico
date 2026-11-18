import {defineConfig, mergeConfig} from 'vite';
import {fileURLToPath} from 'node:url';
import web from '../../web/vite.config.ts';

const entry = fileURLToPath(new URL('./src/main.ts', import.meta.url));
// The web app's own build with one change: the page loads the desktop entry, which puts mpv
// beside the page's own player and then starts the web app as it is.
export default defineConfig(async environment => mergeConfig(await (typeof web === 'function' ? web(environment) : web), {
  root: fileURLToPath(new URL('../../web', import.meta.url)),
  plugins: [{name: 'portico-desktop-entry', transformIndexHtml: {order: 'pre' as const, handler: (html: string) => {
    if (html.split('src="/src/main.tsx"').length !== 2) throw new Error('desktop_entry_missing');
    return html.replace('src="/src/main.tsx"', `src="/@fs/${entry}"`);
  }}}],
  server: {fs: {allow: [fileURLToPath(new URL('../..', import.meta.url))]}},
}));
