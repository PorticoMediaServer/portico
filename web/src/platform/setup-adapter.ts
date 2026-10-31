import {createSetupAdapter, type SetupAdapter, type SetupAdapterDeps} from '@core/setup-adapter.ts';

export type {SetupPairing} from '@core/setup-adapter.ts';
export type WebSetupAdapter = SetupAdapter;
/** The browser's setup adapter: a token for this page, and a Portico Account setup finished by the Return. */
export const createWebSetupAdapter = (deps: Omit<SetupAdapterDeps, 'tokenRoute' | 'finish'>): WebSetupAdapter =>
  createSetupAdapter({...deps, tokenRoute: 'start_browser_setup', finish: {kind: 'return-page'}});
