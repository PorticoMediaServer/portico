import {createSetupAdapter, type SetupAdapter, type SetupAdapterDeps} from '@core/setup-adapter.ts';

export type {SetupPairing} from '@core/setup-adapter.ts';
export type WebSetupAdapter = SetupAdapter;
/** The browser's setup adapter. */
export const createWebSetupAdapter = (deps: SetupAdapterDeps): WebSetupAdapter => createSetupAdapter(deps);
