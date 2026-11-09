// The classic receiver consumes the canonical generated decoder. Keep the full
// runtime/schema graph: browser validation must be identical to other clients.
export { decodeStartPlaybackSessionResponse, decodeGetUserMutationResponse } from '../../packages/contracts/src/decoders.ts';

// The receiver keeps its stop with the present server's older stop protocol. These two files are its own copy of that
// code: the apps no longer use it (they use `packages/client-core/src/outbox.ts`). It goes when Cast moves to the rebuilt server.
export {UserMutationCoordinator, UserMutationError, newUserMutationKey, isUserMutationKey, sameUserMutationScope} from "./user-mutations.ts";
export {IndexedDBUserMutationStore} from "./indexeddb-user-mutation-store.ts";
