// Node test loader: resolves the app's Vite aliases (@core, @design, @) so
// bridge modules can be exercised under node:test without a bundler.
import {register} from 'node:module';
register(new URL('./resolve.mjs', import.meta.url));
