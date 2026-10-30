export type DesktopWebApp=Readonly<{disposeApp():void}>;
/** Native entry owns platform installers; the original app owns its captured
 * viewers. Always retire those real viewers before native/platform teardown. */
export function createDesktopAppLifetime(){
  const life=new AbortController(),cleanups:(()=>void|Promise<void>)[]=[];
  let web:Promise<DesktopWebApp>|undefined,closing:Promise<void>|undefined;
  return Object.freeze({signal:life.signal,
    assertCurrent(){life.signal.throwIfAborted();},
    retain(cleanup:()=>void|Promise<void>){life.signal.throwIfAborted();cleanups.push(cleanup);},
    importWebApp(load:()=>Promise<DesktopWebApp>){
      life.signal.throwIfAborted();if(web)throw new Error('desktop_already_started');web=load();return web;
    },
    dispose():Promise<void>{
      if(closing)return closing;life.abort();
      closing=(async()=>{
        let failed=false;
        // Import evaluation may already have begun. Its own terminal disposer
        // fences initialization before any late provider result can publish.
        if(web)try{(await web).disposeApp();}catch{failed=true;}
        for(const cleanup of cleanups.reverse())try{await cleanup();}catch{failed=true;}
        cleanups.length=0;if(failed)throw new Error('desktop_shutdown_failed');
      })();return closing;
    },
  });
}
