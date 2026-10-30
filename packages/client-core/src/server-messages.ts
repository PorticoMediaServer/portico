/** Viewer-facing copy for a server response that fails its published contract.
 * Codes and diagnostic detail stay with the parser that rejected the response. */
export const unreadableServerResponse = "Your server sent something Portico couldn't read. Check that your server is up to date.";

/** Diagnostic only: the presenter uses server_busy and its existing localized busy message. */
export const guideReadCapacityExceeded = "Guide request capacity exceeded.";

/** A client-side failure with a stable code, so presenters map it by code (X-04) and never by its
 * text. The message stays diagnostic (and matches the plain `Error` it replaced). */
export class CodedError extends Error {
 readonly code:string;
 constructor(code:string,message:string){super(message);this.code=code;}
}
