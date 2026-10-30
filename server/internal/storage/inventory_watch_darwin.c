//go:build darwin && cgo

#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdint.h>
#include <stdlib.h>
extern void portico_watch_event(uintptr_t token,char *path,uint32_t flags);
typedef struct {FSEventStreamRef stream;dispatch_queue_t queue;} PorticoWatch;
static void portico_events(ConstFSEventStreamRef stream,void *info,size_t count,void *paths,const FSEventStreamEventFlags flags[],const FSEventStreamEventId ids[]) {
 char **names=(char **)paths;
 for(size_t i=0;i<count;i++) portico_watch_event((uintptr_t)info,names[i],flags[i]);
}
void *portico_watch_start(char **roots,int count,uintptr_t token) {
 CFMutableArrayRef paths=CFArrayCreateMutable(NULL,count,&kCFTypeArrayCallBacks);
 if(!paths)return NULL;
 for(int i=0;i<count;i++) {CFStringRef path=CFStringCreateWithCString(NULL,roots[i],kCFStringEncodingUTF8);if(path){CFArrayAppendValue(paths,path);CFRelease(path);}}
 FSEventStreamContext context={0,(void *)token,NULL,NULL,NULL};
 PorticoWatch *watch=calloc(1,sizeof(PorticoWatch));if(!watch){CFRelease(paths);return NULL;}
 watch->stream=FSEventStreamCreate(NULL,portico_events,&context,paths,kFSEventStreamEventIdSinceNow,0.5,kFSEventStreamCreateFlagFileEvents|kFSEventStreamCreateFlagWatchRoot|kFSEventStreamCreateFlagNoDefer);
 CFRelease(paths);
 if(!watch->stream){free(watch);return NULL;}
 watch->queue=dispatch_queue_create("portico.folder-watch",DISPATCH_QUEUE_SERIAL);
 FSEventStreamSetDispatchQueue(watch->stream,watch->queue);
 if(!FSEventStreamStart(watch->stream)){FSEventStreamInvalidate(watch->stream);FSEventStreamRelease(watch->stream);dispatch_release(watch->queue);free(watch);return NULL;}
 return watch;
}
static void portico_drain(void *unused){}
void portico_watch_stop(void *pointer) {
 PorticoWatch *watch=pointer;if(!watch)return;
 FSEventStreamStop(watch->stream);FSEventStreamInvalidate(watch->stream);
 dispatch_sync_f(watch->queue,NULL,portico_drain);
 FSEventStreamRelease(watch->stream);dispatch_release(watch->queue);free(watch);
}
