// The classic receiver consumes the canonical generated decoder. Keep the full
// runtime/schema graph: browser validation must be identical to other clients.
export { decodeStartPlaybackSessionResponse, decodeGetUserMutationResponse } from '../../packages/contracts/src/decoders.ts';

export {UserMutationCoordinator, UserMutationError, newUserMutationKey, isUserMutationKey, sameUserMutationScope} from "../../packages/client-core/src/user-mutations.ts";
export {IndexedDBUserMutationStore} from "../../web/src/platform/user-mutations.ts";
