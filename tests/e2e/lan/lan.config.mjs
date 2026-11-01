import {defineConfig} from '../web/node_modules/@playwright/test/index.mjs';

if (!process.env.PORTICO_E2E_LAN_URL) throw new Error('Run with the LAN server harness');
export default defineConfig({
  testDir: '.', testMatch: 'local-setup.spec.mjs', timeout: 120_000, retries: 0,
  outputDir: process.env.PORTICO_E2E_OUTPUT || 'results',
  use: {baseURL: process.env.PORTICO_E2E_LAN_URL, viewport: {width: 1440, height: 900},
    video: 'on', screenshot: 'only-on-failure', trace: 'off'},
});
