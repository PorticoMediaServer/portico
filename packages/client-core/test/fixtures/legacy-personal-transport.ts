// These fixtures exercise the service's projection and command handling.
// personal-saved-next.test.ts covers the rebuilt server's routes and wire documents.
import {personalSavedTransport, type PersonalSavedTransport} from '../../src/personal-saved.ts';
import {resourceContentParams} from '../../src/saved.ts';

export const legacyPersonalTransport = Object.freeze<PersonalSavedTransport>({
  ...personalSavedTransport,
  async history(api, _bound, period, cursor, signal) {
    const query = new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})}); if (period) query.set('period', period);
    return api.request('/fixtures/history?' + query, 'GET', undefined, signal);
  },
  async directory(api, _bound, kind, cursor, signal) {
    const query = new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {}), kind});
    return api.request('/fixtures/resources?' + query, 'GET', undefined, signal);
  },
  async resource(api, _bound, resourceId, asked, cursor, signal) {
    const query = resourceContentParams(new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})}), asked);
    return api.request(`/fixtures/resources/${encodeURIComponent(resourceId)}/content?` + query, 'GET', undefined, signal);
  },
  async send(api, _bound, {action, ...intent}, {resourceId, revision}, operationId, signal) {
    const path = resourceId ? `/fixtures/resources/${encodeURIComponent(resourceId)}` : '/fixtures/resources';
    const [method, suffix] = {
      create: ['POST', ''], update: ['PATCH', ''], delete: ['DELETE', ''],
      entries: ['PUT', '/entries'], reorder: ['PUT', '/order'], pin: ['PUT', '/pin'],
    }[action];
    return api.request(path + suffix, method, {expectedRevision: revision, ...intent, operationId}, signal);
  },
});
