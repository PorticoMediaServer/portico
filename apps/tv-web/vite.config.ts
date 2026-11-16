import {defineConfig, transformWithOxc, type Plugin} from 'vite';
import react from '@vitejs/plugin-react';
import legacy from '@vitejs/plugin-legacy';
import path from 'node:path';
import fs from 'node:fs';
import {createRequire} from 'node:module';

// The screens are the TV app's (`../native/src-next`), read in place and never copied, as the
// Fire TV app does (`../vega/metro.config.js`):
// 1. `react-native` is `src/shims/react-native.ts` everywhere: react-native-web plus the TV
//    exports (focus guides, remote keys, Portico's modules) the shared screens use.
// 2. A shared file that cannot be right in a TV browser is replaced by the file at the same path
//    under `src/overrides/native-next` (or `src/overrides/packages`).
// 3. Every bare import, from anywhere in the repository, resolves to this app's node_modules, so
//    the shared sources never need apps/native's or the root's installs.
const app = import.meta.dirname;
const repo = path.resolve(app, '../..');
const nextSrc = path.join(repo, 'apps/native/src-next');
const packages = path.join(repo, 'packages');
const overrides = path.join(app, 'src/overrides');
const shim = path.join(app, 'src/shims/react-native.ts');
const local = createRequire(path.join(app, 'package.json'));
// Native libraries with no browser build, by the name the shared code imports.
const shimmed: Record<string, string> = {
  '@react-native-community/blur': path.join(app, 'src/shims/blur.tsx'),
  '@react-native-masked-view/masked-view': path.join(app, 'src/shims/masked-view.tsx'),
  'react-native-screens': path.join(app, 'src/shims/screens.tsx'),
  '@react-native/assets-registry/registry': path.join(app, 'src/shims/assets-registry.ts'),
  // The Amazon SDK as the Fire TV bridge uses it, on the browser's own APIs (src/sdk).
  '@amazon-devices/react-native-kepler': path.join(app, 'src/sdk/kepler.ts'),
  '@amazon-devices/react-native-w3cmedia': path.join(app, 'src/sdk/w3cmedia.tsx'),
  '@amazon-devices/keplerscript-netmgr-lib': path.join(app, 'src/sdk/netmgr.ts'),
  '@amazon-devices/keplercrypto': path.join(app, 'src/sdk/crypto.ts'),
  '@amazon-devices/kepler-file-system': path.join(app, 'src/sdk/file-system.ts'),
  '@amazon-devices/react-native-device-info': path.join(app, 'src/sdk/device-info.ts'),
};

const within = (dir: string, file: string) => file === dir || file.startsWith(dir + path.sep);
const vegaSrc = path.join(repo, 'apps/vega/src');
const overrideRoots: [string, string][] = [[nextSrc, path.join(overrides, 'native-next')], [packages, path.join(overrides, 'packages')], [vegaSrc, path.join(overrides, 'vega')]];
function overrideFor(file: string): string | undefined {
  for (const [root, replacements] of overrideRoots) {
    if (!within(root, file)) continue;
    const relative = path.relative(root, file).replace(/\.(tsx?|jsx?)$/, '');
    for (const extension of ['.ts', '.tsx']) {
      const candidate = path.join(replacements, relative + extension);
      if (fs.existsSync(candidate)) return candidate;
    }
  }
  return undefined;
}

function portico(): Plugin {
  return {
    name: 'portico-tv-web',
    enforce: 'pre',
    async resolveId(source, importer, options) {
      if (!importer || source.startsWith('\0')) return null;
      if (source === 'react-native' && importer !== shim) return shim;
      if (shimmed[source]) return shimmed[source];
      if (source.startsWith('@/')) return this.resolve(path.join(nextSrc, source.slice(2)), importer, {...options, skipSelf: true});
      if (source === '@portico/design') return path.join(packages, 'design/src/index.ts');
      if (source === 'react-native-svg' && !importer.includes(`${path.sep}react-native-svg${path.sep}`)) return local.resolve('react-native-svg/lib/module/ReactNativeSVG.web.js');
      if (source.startsWith('.')) {
        const resolved = await this.resolve(source, importer, {...options, skipSelf: true});
        // A replacement may import the file it replaces (its own, or the Fire TV one it reuses).
        const fromReplacement = within(overrides, importer) || within(path.join(vegaSrc, 'overrides'), importer);
        const replacement = resolved && !resolved.external && !fromReplacement ? overrideFor(resolved.id) : undefined;
        if (replacement && replacement !== importer) return replacement;
        return resolved;
      }
      // Bare imports from shared sources outside this app: this app's node_modules.
      if (!within(app, importer) && !source.startsWith('/') && !/^[a-z]+:/.test(source)) {
        return this.resolve(source, path.join(app, 'src/index.tsx'), {...options, skipSelf: true});
      }
      return null;
    },
  };
}

// React Native libraries that publish JSX in `.js` files.
const jsxInJs = /[\\/]node_modules[\\/]react-native-qrcode-svg[\\/].*\.js$/;
function libraryJsx(): Plugin {
  return {
    name: 'portico-library-jsx',
    enforce: 'pre',
    async transform(code, id) {
      if (!jsxInJs.test(id)) return null;
      const result = await transformWithOxc(code, id, {lang: 'jsx', jsx: {runtime: 'automatic'}});
      return {code: result.code, map: result.map};
    },
  };
}

export default defineConfig(({mode}) => ({
  root: app,
  tsconfig: path.join(app, 'tsconfig.build.json'),
  base: './',
  plugins: [portico(), libraryJsx(), react({babel: {plugins: [
    // The shared screens re-render whole rows on every focus move (row focus state, the shell's
    // focus snapshot, the hero preview). The React Compiler memoises components and their props
    // at build time, so an unchanged card is skipped; nothing in the shared sources changes.
    ['babel-plugin-react-compiler', {target: '19'}],
    'react-native-worklets/plugin',
  ]}}),
  // The oldest supported sets (2022) run Chromium 85 (Samsung) and 87 (LG): syntax is compiled down
  // to them (build.target) and the built-in functions they lack (Array.prototype.at,
  // structuredClone…) are added, only those the bundle uses.
  legacy({renderLegacyChunks: false, modernPolyfills: true, modernTargets: ['chrome >= 85']})],
  resolve: {
    extensions: ['.web.tsx', '.web.ts', '.web.js', '.tsx', '.ts', '.mjs', '.js', '.jsx', '.json'],
    dedupe: ['react', 'react-dom', 'react-native-web'],
  },
  define: {
    __DEV__: JSON.stringify(mode !== 'production'),
    __PORTICO_DEV_SIGN_IN__: 'false',
    'process.env.NODE_ENV': JSON.stringify(mode === 'production' ? 'production' : 'development'),
    global: 'globalThis',
  },
  server: {fs: {allow: [repo]}},
  // Development only: PORTICO_TV_PROXY=https://a.server serves its API at this page's own address,
  // so a desktop browser can sign in to a real server without being a TV app.
  preview: process.env.PORTICO_TV_PROXY ? {proxy: {'/v1': {target: process.env.PORTICO_TV_PROXY, changeOrigin: true, secure: true, ws: true}}} : undefined,
  build: {target: 'chrome85', outDir: 'dist', sourcemap: true, chunkSizeWarningLimit: 8000},
}));
