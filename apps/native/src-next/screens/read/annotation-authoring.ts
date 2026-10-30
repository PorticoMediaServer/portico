/** Containment gate for the offline annotation authoring path (P1-READ-OFFLINE-ANNOTATION-SYNC).
 *
 * HOLD — this remains false until the functional journal/merge corrections are reviewed.
 * While false the reader keeps the canonical server annotation port only: no offline
 * canAnnotate synthesis, no journal mutation fallback and no automatic merge flush. Queued
 * private records are preserved untouched; this gate never erases them. */
export const NATIVE_OFFLINE_ANNOTATION_AUTHORING = false;
