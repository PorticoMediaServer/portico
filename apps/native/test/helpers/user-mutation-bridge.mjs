import assert from 'node:assert/strict';
import {sameUserMutationScope, validateUserMutationState} from '../../../../packages/client-core/src/user-mutations.ts';

/** Bounded platform-owner fixture: aggregate CAS and selection changes execute
 * synchronously before resolving, just as one native storage transaction does.
 * Reads and commits cross JSON so callers cannot mutate the stored aggregate. */
export function userMutationBridge() {
  let state = {version: 1, revision: 0, entries: []}, activeScope;
  return {
    async userMutationTransaction(operation, json) {
      const payload = JSON.parse(json);
      if (operation === 'read') return JSON.stringify(state);
      if (operation === 'cas') {
        validateUserMutationState(payload.next);
        assert.equal(payload.next.revision, payload.expectedRevision + 1);
        if (payload.expectedRevision !== state.revision) return false;
        state = structuredClone(payload.next);
        return true;
      }
      assert.ok(operation === 'scope' || operation === 'retireScope', `Unexpected mutation transaction: ${operation}`);
      if (operation === 'retireScope' && (!activeScope || !sameUserMutationScope(activeScope, payload.scope))) return false;
      activeScope = operation === 'scope' ? structuredClone(payload.scope) : undefined;
      const entries = state.entries.map(entry => {
        if (entry.state !== 'pending' || activeScope && sameUserMutationScope(entry.scope, activeScope)) return entry;
        const next = {...entry, state: 'quarantined', errorCode: 'mutation_scope_changed'};
        delete next.leaseOwner; delete next.leaseUntilMs;
        return next;
      });
      state = validateUserMutationState({...state, revision: state.revision + 1, entries});
      return true;
    },
  };
}
