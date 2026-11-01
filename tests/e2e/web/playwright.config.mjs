import {defineConfig} from '@playwright/test';

if (!process.env.PORTICO_E2E_SERVER_URL) throw new Error('Run through the E2E server harness');
export default defineConfig({
  testDir: '.', testMatch: 'pilot.spec.mjs', timeout: 120_000, retries: 0,
  outputDir: process.env.PORTICO_E2E_OUTPUT || 'results',
  use: {baseURL: 'http://127.0.0.1:5174', viewport: {width: 1440, height: 900},
    video: 'on', screenshot: 'only-on-failure', trace: 'off'},
  webServer: {command: 'npm run dev:web', cwd: '../../..', url: 'http://127.0.0.1:5174',
    timeout: 120_000, reuseExistingServer: false,
    env: {...process.env, PORTICO_DEV_SERVER_URL: process.env.PORTICO_E2E_SERVER_URL}},
});
