#import "../Native/PorticoQueueTransport.h"
#import <sys/socket.h>
#import <netinet/in.h>
#import <unistd.h>
#import <mach/mach.h>
#import <errno.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengePerformDefaultHandling,nil);}
+ (BOOL)isRefusal:(NSError *)error{return NO;}
@end
#pragma clang diagnostic pop

static NSUInteger checks;
static void check(BOOL ok,const char *what){checks++;if(!ok){fprintf(stderr,"FAIL %s\n",what);exit(1);}}
static BOOL waitFor(BOOL (^ready)(void)){
 NSDate *until=[NSDate dateWithTimeIntervalSinceNow:8];
 while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];
 return ready();
}
static BOOL sendAll(int peer,const void *bytes,NSUInteger length){
 NSUInteger sent=0;while(sent<length){ssize_t next=send(peer,(const uint8_t *)bytes+sent,length-sent,0);if(next<=0)return NO;sent+=(NSUInteger)next;}return YES;
}
static uint64_t residentBytes(void){
 mach_task_basic_info_data_t info={0};mach_msg_type_number_t count=MACH_TASK_BASIC_INFO_COUNT;
 return task_info(mach_task_self(),MACH_TASK_BASIC_INFO,(task_info_t)&info,&count)==KERN_SUCCESS?info.resident_size:0;
}

static void pausedSource(uint64_t total,BOOL chunked,BOOL consumeFirst){
 int listener=socket(AF_INET,SOCK_STREAM,0);struct sockaddr_in address={0};address.sin_family=AF_INET;address.sin_addr.s_addr=htonl(INADDR_LOOPBACK);
 check(listener>=0&&bind(listener,(struct sockaddr *)&address,sizeof(address))==0&&listen(listener,1)==0,"bounded test listener");
 socklen_t length=sizeof(address);getsockname(listener,(struct sockaddr *)&address,&length);
 dispatch_group_t served=dispatch_group_create();NSObject *progress=[NSObject new];__block uint64_t sent=0;__block int peerError=0;
 dispatch_group_async(served,dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  int peer=accept(listener,NULL,NULL);if(peer<0)return;int yes=1;setsockopt(peer,SOL_SOCKET,SO_NOSIGPIPE,&yes,sizeof(yes));
  struct timeval timeout={3,0};setsockopt(peer,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));setsockopt(peer,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
  char request[8192]={0};ssize_t used=0;while(used<(ssize_t)sizeof(request)-1&&!strstr(request,"\r\n\r\n")){ssize_t n=recv(peer,request+used,sizeof(request)-1-used,0);if(n<=0)break;used+=n;}
  char header[256];
  if(chunked)snprintf(header,sizeof(header),"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Type: application/pdf\r\nConnection: close\r\n\r\n");
  else snprintf(header,sizeof(header),"HTTP/1.1 200 OK\r\nContent-Length: %llu\r\nContent-Type: application/pdf\r\nConnection: close\r\n\r\n",(unsigned long long)total);
  sendAll(peer,header,strlen(header));uint8_t bytes[16384];for(NSUInteger i=0;i<sizeof(bytes);i++)bytes[i]=(uint8_t)i;
  uint64_t current=0;
  while(current<total){
   NSUInteger amount=(NSUInteger)MIN((uint64_t)sizeof(bytes),total-current);char size[24];int n=snprintf(size,sizeof(size),"%lx\r\n",(unsigned long)amount);
   if(chunked&&!sendAll(peer,size,(NSUInteger)n)){@synchronized(progress){peerError=errno;}break;}
   if(!sendAll(peer,bytes,amount)){@synchronized(progress){peerError=errno;}break;}current+=amount;@synchronized(progress){sent=current;}
   if(chunked&&!sendAll(peer,"\r\n",2)){@synchronized(progress){peerError=errno;}break;}
  }
  if(chunked&&current==total)sendAll(peer,"0\r\n\r\n",5);
  shutdown(peer,SHUT_RDWR);close(peer);
 });
 NSObject *owner=[NSObject new];NSMutableDictionary *state=[NSMutableDictionary new];
 NSString *url=[NSString stringWithFormat:@"http://127.0.0.1:%u/v1/reading/sessions/prefetch/source",ntohs(address.sin_port)];
 uint64_t before=residentBytes();
 PorticoQueueRequest *request=PorticoReadingStreamRequest(NSUUID.UUID.UUIDString,url,@{},owner,
  ^(id value){@synchronized(state){state[@"headers"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
 [PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_stream_busy"];
 check(waitFor(^BOOL{@synchronized(state){return state[@"headers"]||state[@"error"];}})&&!state[@"error"],"first-byte open metadata");
 @synchronized(state){check([state[@"headers"][@"status"]isEqual:@200]&&[state[@"headers"] count]==2,"open never returns body bytes");}
 if(consumeFirst){
  PorticoReadingStreamRead(request.identifier,owner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
  check(waitFor(^BOOL{@synchronized(state){return state[@"read"]||state[@"error"];}})&&!state[@"error"],"one explicit bounded pull");
  @synchronized(state){
   NSData *data=[[NSData alloc]initWithBase64EncodedString:state[@"read"][@"bodyBase64"] options:0];
   check(data.length>0&&data.length<=PorticoStreamPullLimit&&[state[@"read"][@"done"]isEqual:@NO],"first pull is neither oversized nor EOF");
   [state removeObjectForKey:@"read"];
  }
 }
 [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.35]];
 uint64_t paused=residentBytes(),written;@synchronized(progress){written=sent;}
 @synchronized(request){
  check(!request.completed&&request.streamDriver&&request.data.length==0&&!request.streamResolve&&!request.streamPullInFlight,"paused reader retains no application body buffer or pending pull");
  check(PorticoQueueActive==1&&PorticoQueueReads==1&&PorticoQueueRequests.count==1,"paused source has one admitted request, not growing queued reads");
  check(request.task.state!=NSURLSessionTaskStateCompleted&&written<total,"paused source does not complete or cache the whole upstream file");
 }
 printf("Paused source total=%llu consumed=%u sent=%llu applicationBuffer=0 requests=1 residentDelta=%lld\n",(unsigned long long)total,consumeFirst,(unsigned long long)written,(long long)paused-(long long)before);
 NSURLSessionTask *task=request.task;
 if(consumeFirst)[PorticoQueueTransport cancelOwnedBy:owner];else{@synchronized(request){request.failureCode=@"cancelled";[request finish:@"network_stream_cancelled"];}}
 __block NSString *failure=nil;__block BOOL bodyReturned=NO;
 PorticoReadingStreamRead(request.identifier,owner,^(id value){bodyReturned=YES;},^(NSString *c,NSString *m,NSError *e){failure=c;});
 check([failure isEqual:@"cancelled"]&&!bodyReturned,"same-id/owner cancellation cannot become fake EOF");
 close(listener);BOOL closed=dispatch_group_wait(served,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))==0;
 BOOL taskClosed=task.state==NSURLSessionTaskStateCanceling||task.state==NSURLSessionTaskStateCompleted;
 [[PorticoQueueTransport sessionFor:NO] getAllTasksWithCompletionHandler:^(NSArray<NSURLSessionTask *> *tasks){@synchronized(state){state[@"remainingTasks"]=@(tasks.count);}}];
 BOOL taskList=waitFor(^BOOL{@synchronized(state){return state[@"remainingTasks"]!=nil;}});
 BOOL noTasks=taskList&&[state[@"remainingTasks"] isEqual:@0];
 int socketError;@synchronized(progress){socketError=peerError;}
 BOOL peerClosed=socketError==EPIPE||socketError==ECONNRESET||socketError==ENOTCONN;
 if(!closed||!taskClosed||!noTasks||!peerClosed)fprintf(stderr,"Paused cancellation peerClosed=%d peerError=%d taskCancelled=%d taskState=%lu sessionTasks=%ld\n",closed,socketError,taskClosed,(unsigned long)task.state,taskList?[state[@"remainingTasks"] integerValue]:-1);
 check(closed&&peerClosed&&taskClosed&&noTasks,"cancellation closes peer and leaves no outstanding session task");
 check(request.completed&&!request.data&&!request.response&&!request.wire&&!request.streamDriver&&!PorticoQueueRequests.count&&PorticoQueueActive==0&&PorticoQueueReads==0,"cancellation releases retained data, authority-bearing request and all admission");
}

int main(void){@autoreleasepool{
 NSURLSessionConfiguration *configuration=[PorticoQueueTransport sessionFor:NO].configuration;
 check(!configuration.URLCache&&!configuration.HTTPCookieStorage&&!configuration.URLCredentialStorage&&!configuration.HTTPShouldSetCookies,"existing ephemeral transport has no file/cache or credential-store writer");
 pausedSource(32*1024*1024+17,NO,NO);
 pausedSource(128*1024*1024+17,YES,NO);
 pausedSource(128*1024*1024+17,NO,YES);
 printf("Apple paused-reader bounds: %lu checks passed\n",(unsigned long)checks);return 0;
}}
