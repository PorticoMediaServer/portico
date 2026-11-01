import {defineConfig} from '@playwright/test';

export default defineConfig({
  testDir: '.', testMatch: 'downloads.spec.mjs', timeout: 120_000, retries: 0,
  outputDir: process.env.PORTICO_E2E_OUTPUT || 'results',
  use: {baseURL: 'http://127.0.0.1:19200', viewport: {width: 1440, height: 900},
    video: 'on', screenshot: 'only-on-failure', trace: 'off'},
  webServer: {command: 'npm run dev', cwd: '../../../website', url: 'http://127.0.0.1:19200/download',
    timeout: 120_000, reuseExistingServer: false},
});
