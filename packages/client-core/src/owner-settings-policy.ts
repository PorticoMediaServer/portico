import type {HttpLocalApi} from './index.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {connectivityForm} from './server-admin/server-forms.ts';
export const readOwnerOptionalConfiguration = <T = unknown>(api: Pick<HttpLocalApi, 'request'>, signal: AbortSignal): Promise<T> => api.request<T>(routePath('get_settings_configuration'), 'GET', undefined, signal, {manageGate: false});
export const ownerAwayLimitForm = (deps: Parameters<typeof connectivityForm>[0], api: Pick<HttpLocalApi, 'request'>) => connectivityForm({...deps, api: {request: (path, method, body, signal) => api.request(path, method, body, signal, {manageGate: false})}});
