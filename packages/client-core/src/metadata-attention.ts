/** The shapes of a library's titles that need attention (unmatched or unsure), as the owner's attention words read them. */
export type AttentionIdentity=Readonly<{provider:'tmdb'|'tvdb'|'anilist';type:'movie'|'show'|'anime'|'episode';id:string}>;
export type AttentionCandidate=Readonly<{posterUrl?:string;key:string;identity:AttentionIdentity;title:string;year:number;confidence:number;reasons:readonly string[];orders:readonly Readonly<{id:string;name:string}>[]}>;
/** Film and TV titles are 'item' or 'show'; music and audiobooks are 'album', 'song' or 'book'. */
export type AttentionKind='item'|'show'|'album'|'song'|'book';
export type AttentionEntry=Readonly<{artworkUrl?:string;parentTitle?:string;parentArtist?:string;libraryId:string;entityId:string;kind:AttentionKind;itemKind:string;parentId:string;actions:readonly string[];title:string;revision:number;reason:string;confidence:number;candidates:readonly AttentionCandidate[]}>;
export type AttentionPage=Readonly<{libraryId:string;total:number;entries:readonly AttentionEntry[];nextCursor:string}>;
export type AttentionCommand=Readonly<{kind:AttentionKind;entityId:string;expectedRevision:number;action:'accept'|'search'|'id'|'dismiss'|'retry';candidateKey?:string;provider?:AttentionIdentity['provider'];providerId?:string;query?:string;order?:string}>;
