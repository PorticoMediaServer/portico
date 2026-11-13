/** Cast needs matching receiver ids in the native build and the server build.
 * Empty ordinary builds offer nothing; no owner configures or registers an id. */
export function castOffered(buildCanCast:boolean,serverApplication:unknown,buildApplication:unknown):boolean{
 if(!buildCanCast)return false;
 const server=typeof serverApplication==='string'?serverApplication.trim():'';
 const built=typeof buildApplication==='string'?buildApplication.trim():'';
 return !!server&&!!built&&server.toUpperCase()===built.toUpperCase();
}
