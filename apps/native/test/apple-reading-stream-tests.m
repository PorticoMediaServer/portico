#import "../Native/PorticoQueueTransport.h"
#import <sys/socket.h>
#import <netinet/in.h>
#import <unistd.h>
#import <errno.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengePerformDefaultHandling,nil);}
+ (BOOL)isRefusal:(NSError *)error {return [error.domain isEqual:@"test_refused_certificate"];}
@end
#pragma clang diagnostic pop

@interface ReadingStreamTask : NSURLSessionDataTask
@property(nonatomic,copy) NSString *testID;
@property(nonatomic) NSUInteger resumes,suspends,cancels;
@property(nonatomic) NSURLSessionTaskState testState;
@end
@implementation ReadingStreamTask
- (NSString *)taskDescription{return _testID;}
- (NSURLSessionTaskState)state{return _testState;}
- (void)suspend{_suspends++;_testState=NSURLSessionTaskStateSuspended;}
- (void)resume{_resumes++;_testState=NSURLSessionTaskStateRunning;}
- (void)cancel{_cancels++;_testState=NSURLSessionTaskStateCanceling;}
@end

@interface ReadingStreamDriver : NSObject<PorticoReadingStreamDriver>
@property(nonatomic,copy) void (^pending)(NSData *,BOOL,NSError *);
@property(nonatomic) NSUInteger reads,cancels;
@end
@implementation ReadingStreamDriver
- (void)openWithSession:(NSURLSession *)session request:(NSURLRequest *)request attached:(void (^)(NSURLSessionTask *))attached headers:(void (^)(NSHTTPURLResponse *))headers failure:(void (^)(NSError *))failure {}
- (void)readWithCompletion:(void (^)(NSData *,BOOL,NSError *))completion {_reads++;_pending=completion;}
- (void)cancel{_cancels++;_pending=nil;}
@end

static NSUInteger checks,opened,openRejected;
static NSDictionary *opening;
static NSString *openError;
static NSObject *owner;
static PorticoQueueTransport *transport;
static void check(BOOL value,const char *name){checks++;if(!value){fprintf(stderr,"FAIL %s\n",name);exit(1);}}
static PorticoQueueRequest *make(void){
 opened=openRejected=0;opening=nil;openError=nil;
 return PorticoReadingStreamRequest(NSUUID.UUID.UUIDString,@"https://server.example/v1/reading/sessions/s-1/source",@{@"If-Match":@"\"revision\"",@"Authorization":@"synthetic-test-token"},owner,
  ^(id value){opened++;opening=value;},^(NSString *code,NSString *message,NSError *error){openRejected++;openError=code;});
}
static ReadingStreamTask *install(PorticoQueueRequest *request){
 ReadingStreamTask *task=[ReadingStreamTask new];task.testID=request.identifier;task.testState=NSURLSessionTaskStateRunning;request.task=task;request.started=YES;
 request.streamDriver=[ReadingStreamDriver new];
 if(!PorticoQueueRequests)PorticoQueueRequests=[NSMutableDictionary new];
 PorticoQueueRequests[request.identifier]=request;PorticoQueueActive++;PorticoQueueReads++;return task;
}
static void headers(ReadingStreamTask *task,NSInteger status){
 NSHTTPURLResponse *response=[[NSHTTPURLResponse alloc]initWithURL:[NSURL URLWithString:@"https://server.example/v1/reading/sessions/s-1/source"] statusCode:status HTTPVersion:@"HTTP/1.1" headerFields:@{@"Content-Length":status==403?@"6":@"100000",@"Content-Type":@"application/pdf",@"ETag":@"\"revision\""}];
 [PorticoQueueRequests[task.testID] openStreamResponse:response];
}
static void bytes(PorticoQueueRequest *request,NSData *data,BOOL eof,NSError *error){ReadingStreamDriver *driver=(id)request.streamDriver;void (^pending)(NSData *,BOOL,NSError *)=driver.pending;driver.pending=nil;check(pending!=nil,"only an actual pending pull receives bytes");pending(data,eof,error);}
static void pull(PorticoQueueRequest *request,NSMutableArray *replies,NSMutableArray *errors){
 PorticoReadingStreamRead(request.identifier,owner,^(id value){[replies addObject:value];},^(NSString *code,NSString *message,NSError *error){[errors addObject:code];});
}
static void cancel(PorticoQueueRequest *request){request.failureCode=@"cancelled";[request finish:@"network_stream_cancelled"];}

static BOOL waitFor(BOOL (^ready)(void)) {
 NSDate *until=[NSDate dateWithTimeIntervalSinceNow:8];
 while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];
 return ready();
}
static BOOL waitForLimit(NSTimeInterval limit,BOOL (^ready)(void)){
 NSDate *until=[NSDate dateWithTimeIntervalSinceNow:limit];while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];return ready();
}
/** Real NSURLSession data/delegate scheduling and suspension, without a device,
 * auth state or TLS claim. The loopback peer generates bytes, never stores a PDF. */
static BOOL sendAll(int peer,const void *bytes,NSUInteger length) {
 NSUInteger sent=0;while(sent<length){ssize_t next=send(peer,(const uint8_t *)bytes+sent,length-sent,0);if(next<=0)return NO;sent+=(NSUInteger)next;}return YES;
}
static void actualPullStream(BOOL chunked) {
 const NSUInteger total=2097152+17;
 int listener=socket(AF_INET,SOCK_STREAM,0);check(listener>=0,"loopback socket");
 struct sockaddr_in address={0};address.sin_family=AF_INET;address.sin_addr.s_addr=htonl(INADDR_LOOPBACK);
 check(bind(listener,(struct sockaddr *)&address,sizeof(address))==0&&listen(listener,1)==0,"private ephemeral loopback listener");
 socklen_t size=sizeof(address);check(getsockname(listener,(struct sockaddr *)&address,&size)==0,"actual loopback port");
 dispatch_group_t served=dispatch_group_create();
 dispatch_group_async(served,dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  int peer=accept(listener,NULL,NULL);if(peer<0)return;
  int yes=1;setsockopt(peer,SOL_SOCKET,SO_NOSIGPIPE,&yes,sizeof(yes));
  struct timeval timeout={5,0};setsockopt(peer,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));setsockopt(peer,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
  char request[8192]={0};ssize_t count=0;
  while(count<(ssize_t)sizeof(request)-1&&!strstr(request,"\r\n\r\n")){ssize_t next=recv(peer,request+count,sizeof(request)-1-count,0);if(next<=0)break;count+=next;}
  char header[256];if(chunked)snprintf(header,sizeof(header),"HTTP/1.1 200 OK\r\nContent-Type: application/pdf\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n");else snprintf(header,sizeof(header),"HTTP/1.1 200 OK\r\nContent-Type: application/pdf\r\nContent-Length: %lu\r\nConnection: close\r\n\r\n",(unsigned long)total);
  if(strstr(request,"GET /v1/reading/sessions/live/source HTTP/1.1")&&!strstr(request,"Range:")){
   sendAll(peer,header,strlen(header));unsigned char bytes[16384];for(NSUInteger i=0;i<sizeof(bytes);i++)bytes[i]=(uint8_t)i;
   NSUInteger sent=0;
   while(sent<total){NSUInteger amount=MIN(sizeof(bytes),total-sent);char size[24];int n=snprintf(size,sizeof(size),"%lx\r\n",(unsigned long)amount);if(chunked&&!sendAll(peer,size,(NSUInteger)n))break;if(!sendAll(peer,bytes,amount))break;sent+=amount;if(chunked&&!sendAll(peer,"\r\n",2))break;}
   if(chunked&&sent==total)sendAll(peer,"0\r\n\r\n",5);
  }
  shutdown(peer,SHUT_RDWR);close(peer);
 });
 NSMutableDictionary *state=[NSMutableDictionary new];NSObject *realOwner=[NSObject new];
 NSString *url=[NSString stringWithFormat:@"http://127.0.0.1:%u/v1/reading/sessions/live/source",ntohs(address.sin_port)];
 PorticoQueueRequest *request=PorticoReadingStreamRequest(NSUUID.UUID.UUIDString,url,@{},realOwner,^(id value){@synchronized(state){state[@"headers"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;state[@"failure"]=m;}});
 [PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_stream_busy"];
 BOOL opened=waitFor(^BOOL{@synchronized(state){return state[@"headers"]||state[@"error"];}});
 check(opened&&!state[@"error"]&&[state[@"headers"][@"status"]isEqual:@200],"actual task resolves headers before read");
 [NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.1]];
 @synchronized(request){check(!request.completed&&request.streamDriver&&request.data.length==0,"real same-session byte reader keeps no prefetched body window");}
 NSUInteger received=0,pulls=0;BOOL done=NO,valid=YES;
 while(!done&&pulls++<512){
  @synchronized(state){[state removeObjectForKey:@"read"];}
  PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;state[@"failure"]=m;}});
  if(!waitFor(^BOOL{@synchronized(state){return state[@"read"]||state[@"error"];}})){valid=NO;break;}
  NSDictionary *reply;@synchronized(state){if(state[@"error"]){valid=NO;break;}reply=state[@"read"];}
  NSData *bytes=[[NSData alloc]initWithBase64EncodedString:reply[@"bodyBase64"] options:0];done=[reply[@"done"]isEqual:@YES];
  if(!bytes||(done?bytes.length!=0:bytes.length==0||bytes.length>PorticoStreamPullLimit)){valid=NO;break;}
  const uint8_t *raw=bytes.bytes;for(NSUInteger i=0;i<bytes.length;i++)if(raw[i]!=(uint8_t)(received+i)){valid=NO;break;}
  received+=bytes.length;
  @synchronized(request){if(request.data.length>PorticoStreamChunkLimit){valid=NO;break;}}
 }
 if(!done)cancel(request);
 close(listener);BOOL serverClosed=dispatch_group_wait(served,dispatch_time(DISPATCH_TIME_NOW,6*NSEC_PER_SEC))==0;
 if(!valid||!done||received!=total)fprintf(stderr,"Real stream: valid=%d done=%d received=%lu pulls=%lu error=%s failure=%s\n",valid,done,(unsigned long)received,(unsigned long)pulls,[state[@"error"] UTF8String]?:"none",[state[@"failure"] UTF8String]?:"none");
 check(valid&&done&&received==total&&pulls>42,"real two-MiB plus17 source arrives as exact demand-driven <=48KiB pulls and genuine EOF");
 check(serverClosed&&!PorticoQueueRequests[request.identifier]&&PorticoQueueActive==0&&PorticoQueueReads==0,"real peer, request and admission close");
}

static void actualCancel(NSUInteger mode) {
 int listener=socket(AF_INET,SOCK_STREAM,0);struct sockaddr_in address={0};address.sin_family=AF_INET;address.sin_addr.s_addr=htonl(INADDR_LOOPBACK);
 check(listener>=0&&bind(listener,(struct sockaddr *)&address,sizeof(address))==0&&listen(listener,1)==0,"cancellation listener");
 socklen_t length=sizeof(address);getsockname(listener,(struct sockaddr *)&address,&length);
 dispatch_semaphore_t accepted=dispatch_semaphore_create(0),release=dispatch_semaphore_create(0);dispatch_group_t served=dispatch_group_create();
 __block BOOL disconnected=NO;
 dispatch_group_async(served,dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  int peer=accept(listener,NULL,NULL);if(peer<0)return;int yes=1;setsockopt(peer,SOL_SOCKET,SO_NOSIGPIPE,&yes,sizeof(yes));
  struct timeval timeout={3,0};setsockopt(peer,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));
  char request[8192]={0};ssize_t count=0;while(count<(ssize_t)sizeof(request)-1&&!strstr(request,"\r\n\r\n")){ssize_t next=recv(peer,request+count,sizeof(request)-1-count,0);if(next<=0)break;count+=next;}
  const char *header="HTTP/1.1 200 OK\r\nContent-Length: 33554432\r\nConnection: close\r\n\r\n";sendAll(peer,header,strlen(header));
  if(mode!=1){uint8_t initial[49152]={0};sendAll(peer,initial,sizeof(initial));}
  dispatch_semaphore_signal(accepted);
  dispatch_semaphore_wait(release,dispatch_time(DISPATCH_TIME_NOW,6*NSEC_PER_SEC));
  char byte;ssize_t end=recv(peer,&byte,1,0);disconnected=end==0||(end<0&&errno==ECONNRESET);shutdown(peer,SHUT_RDWR);close(peer);
 });
 NSObject *realOwner=[NSObject new];NSMutableDictionary *state=[NSMutableDictionary new];
 NSString *url=[NSString stringWithFormat:@"http://127.0.0.1:%u/v1/reading/sessions/cancel/source",ntohs(address.sin_port)];
 PorticoQueueRequest *request=PorticoReadingStreamRequest(NSUUID.UUID.UUIDString,url,@{},realOwner,^(id value){@synchronized(state){state[@"headers"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
 [PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_stream_busy"];
 BOOL began=dispatch_semaphore_wait(accepted,dispatch_time(DISPATCH_TIME_NOW,6*NSEC_PER_SEC))==0;
 check(began,"real connection opened before cancellation");
 if(mode!=1){
  BOOL gotHeaders=waitFor(^BOOL{@synchronized(state){return state[@"headers"]||state[@"error"];}});
  if(!gotHeaders||state[@"error"])fprintf(stderr,"Cancellation mode=%lu headers=%d nativeResponse=%d error=%s\n",(unsigned long)mode,state[@"headers"]!=nil,request.response!=nil,[state[@"error"] UTF8String]?:"none");
  check(gotHeaders&&!state[@"error"],"first upstream byte resolves actual response metadata");
  PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
  check(waitFor(^BOOL{@synchronized(state){return state[@"read"]||state[@"error"];}})&&!state[@"error"],"first explicit read returns the native-held initial chunk");
  @synchronized(state){NSData *first=[[NSData alloc]initWithBase64EncodedString:state[@"read"][@"bodyBase64"] options:0];check(first.length==49152&&[state[@"read"][@"done"]isEqual:@NO],"initial chunk remains exactly bounded and is not EOF");[state removeObjectForKey:@"read"];}
  PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
  @synchronized(request){check(request.streamResolve!=nil&&!request.completed,"real body pull pending without upstream bytes");}
 }
 if(mode==3)[PorticoQueueTransport cancelOwnedBy:realOwner];else cancel(request);
 dispatch_semaphore_signal(release);
 check(waitFor(^BOOL{@synchronized(state){return state[@"error"]!=nil;}})&&[state[@"error"]isEqual:@"cancelled"]&&!state[@"read"],"real cancellation rejects rather than reporting EOF");
 close(listener);BOOL closed=dispatch_group_wait(served,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))==0;
 check(closed&&disconnected&&!PorticoQueueRequests[request.identifier]&&PorticoQueueActive==0&&PorticoQueueReads==0,"real opening/read/owner cancellation closes peer and admission");
}

static void actualResponse(NSInteger status,NSString *text,BOOL delayed,BOOL silent){
 NSData *body=[text dataUsingEncoding:NSUTF8StringEncoding];
 int listener=socket(AF_INET,SOCK_STREAM,0);struct sockaddr_in address={0};address.sin_family=AF_INET;address.sin_addr.s_addr=htonl(INADDR_LOOPBACK);
 check(listener>=0&&bind(listener,(struct sockaddr *)&address,sizeof(address))==0&&listen(listener,1)==0,"response fixture listener");
 socklen_t length=sizeof(address);getsockname(listener,(struct sockaddr *)&address,&length);
 dispatch_group_t served=dispatch_group_create();dispatch_semaphore_t headersSent=dispatch_semaphore_create(0),release=dispatch_semaphore_create(0);
 __block BOOL clientClosed=NO;
 dispatch_group_async(served,dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
  int peer=accept(listener,NULL,NULL);if(peer<0)return;int yes=1;setsockopt(peer,SOL_SOCKET,SO_NOSIGPIPE,&yes,sizeof(yes));struct timeval timeout={3,0};setsockopt(peer,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));
  char request[8192]={0};ssize_t used=0;while(used<(ssize_t)sizeof(request)-1&&!strstr(request,"\r\n\r\n")){ssize_t n=recv(peer,request+used,sizeof(request)-1-used,0);if(n<=0)break;used+=n;}
  char header[256];NSUInteger declared=silent?33554432:(status==304?4096:body.length);
  snprintf(header,sizeof(header),"HTTP/1.1 %ld Result\r\nContent-Length: %lu\r\nConnection: close\r\n\r\n",(long)status,(unsigned long)declared);sendAll(peer,header,strlen(header));dispatch_semaphore_signal(headersSent);
  if(delayed||silent)dispatch_semaphore_wait(release,dispatch_time(DISPATCH_TIME_NOW,(silent?21:6)*NSEC_PER_SEC));
  if(silent){char byte;ssize_t end=recv(peer,&byte,1,0);clientClosed=end==0||(end<0&&errno==ECONNRESET);}else if(body.length)sendAll(peer,body.bytes,body.length);
  shutdown(peer,SHUT_RDWR);close(peer);
 });
 NSObject *realOwner=[NSObject new];NSMutableDictionary *state=[NSMutableDictionary new];
 NSString *url=[NSString stringWithFormat:@"http://127.0.0.1:%u/v1/reading/sessions/response/source",ntohs(address.sin_port)];
 PorticoQueueRequest *request=PorticoReadingStreamRequest(NSUUID.UUID.UUIDString,url,@{},realOwner,^(id value){@synchronized(state){state[@"headers"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
 [PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_stream_busy"];
 check(dispatch_semaphore_wait(headersSent,dispatch_time(DISPATCH_TIME_NOW,6*NSEC_PER_SEC))==0,"wire headers sent before optional body delay");
 if(silent){
  __block BOOL requested=NO;
  BOOL timed=waitForLimit(18,^{BOOL metadata,error;@synchronized(state){metadata=state[@"headers"]!=nil;error=state[@"error"]!=nil;}
   if(metadata&&!requested&&!error){requested=YES;PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});}return error;
  });
  dispatch_semaphore_signal(release);
  check(timed&&[state[@"error"]isEqual:@"timeout"]&&!state[@"read"],"headers plus silence uses existing timeout, never empty success or EOF");
 }else{
  if(delayed){[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.1]];check(!state[@"read"],"delayed body never arrives before explicit read");dispatch_semaphore_signal(release);}
  check(waitFor(^BOOL{@synchronized(state){return state[@"headers"]||state[@"error"];}})&&!state[@"error"],"first-byte or end-of-body metadata resolves");
  check([state[@"headers"][@"status"]isEqual:@(status)]&&[state[@"headers"] count]==2,"actual status/headers preserved without body reinterpretation");
  PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
  check(waitFor(^BOOL{@synchronized(state){return state[@"read"]||state[@"error"];}})&&!state[@"error"],"empty/delayed/error body read settles");
  if(body.length){
   NSData *received=[[NSData alloc]initWithBase64EncodedString:state[@"read"][@"bodyBase64"] options:0];check([received isEqual:body]&&[state[@"read"][@"done"]isEqual:@NO],"first explicit read returns exactly the native-held body bytes");
   @synchronized(state){[state removeObjectForKey:@"read"];}
   PorticoReadingStreamRead(request.identifier,realOwner,^(id value){@synchronized(state){state[@"read"]=value;}},^(NSString *c,NSString *m,NSError *e){@synchronized(state){state[@"error"]=c;}});
   check(waitFor(^BOOL{@synchronized(state){return state[@"read"]||state[@"error"];}})&&!state[@"error"],"body is followed by actual EOF");
  }
  check([state[@"read"]isEqual:@{@"done":@YES,@"bodyBase64":@""}],"empty response or final read is exactly empty EOF");
 }
 close(listener);BOOL closed=dispatch_group_wait(served,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))==0;
 check(closed&&(!silent||clientClosed)&&request.completed&&!request.data&&!request.streamDriver&&!PorticoQueueRequests.count&&PorticoQueueActive==0&&PorticoQueueReads==0,"response EOF/timeout closes peer and releases request admission");
}

int main(void){@autoreleasepool{
 transport=[PorticoQueueTransport new];owner=[NSObject new];
 for(NSString *url in @[@"https://server.example/v1/reading/sessions/s/source?x=1",@"https://server.example/v1/reading/sessions/s/assets",@"https://server.example/v1/reading/sessions/s/pages/0",@"https://server.example/v1/reading/sessions/s/%73ource",@"https://server.example/v1/system",@"file:///v1/reading/sessions/s/source",@"https://user:pass@server.example/v1/reading/sessions/s/source",@"https://server.example/v1/reading/sessions/s/source#f"]){
  __block NSUInteger failures=0;PorticoQueueRequest *bad=PorticoReadingStreamRequest(@"id",url,@{},owner,^(id value){},^(NSString *c,NSString *m,NSError *e){failures++;});check(!bad&&failures==1,"source-only canonical URL rejected before any task");
 }
 for(NSString *name in @[@"Range",@"range",@"rAnGe"]){
  __block NSUInteger failures=0;check(!PorticoReadingStreamRequest(@"id",@"https://server.example/v1/reading/sessions/s/source",@{name:@"bytes=0-4"},owner,^(id value){},^(NSString *c,NSString *m,NSError *e){failures++;})&&failures==1,"Range never enters sequential stream");
 }
 PorticoQueueRequest *request=make();ReadingStreamTask *task=install(request);headers(task,200);
 check(opened==1&&!openRejected&&[opening[@"status"]isEqual:@200]&&[opening[@"headers"] count]==3&&!opening[@"bodyBase64"],"open resolves actual metadata only");
 check(request.wire.timeoutInterval==15&&[request.wire.HTTPMethod isEqual:@"GET"]&&!request.wire.HTTPBody&&[[request.wire valueForHTTPHeaderField:@"If-Match"]isEqual:@"\"revision\""],"same deadline, captured precondition and GET request");
 check(request.data.length==0&&[(ReadingStreamDriver *)request.streamDriver reads]==0,"metadata never asks the body driver to prefetch");
 NSMutableArray *replies=[NSMutableArray new],*errors=[NSMutableArray new];
 pull(request,replies,errors);check(!replies.count&&!errors.count&&[(ReadingStreamDriver *)request.streamDriver reads]==1,"one pull reads the captured driver");
 pull(request,replies,errors);check(errors.count==1&&[errors.lastObject isEqual:@"queue_transport"]&&[(ReadingStreamDriver *)request.streamDriver reads]==1,"second pending pull rejects without replacing first");[errors removeAllObjects];
 NSMutableData *source=[NSMutableData dataWithLength:100000];for(NSUInteger i=0;i<source.length;i++)((uint8_t *)source.mutableBytes)[i]=(uint8_t)i;
 NSMutableData *received=[NSMutableData new];NSUInteger offset=0;
 while(offset<source.length){
  NSUInteger length=MIN(PorticoStreamPullLimit,source.length-offset);
  bytes(request,[source subdataWithRange:NSMakeRange(offset,length)],NO,nil);
  NSDictionary *reply=replies.lastObject;check([reply[@"done"]isEqual:@NO],"received bytes never claim EOF");
  [received appendData:[[NSData alloc]initWithBase64EncodedString:reply[@"bodyBase64"] options:0]];
  check(request.data.length==0&&!request.streamResolve&&!request.streamPullInFlight,"one reply releases all application body state");
  offset+=length;if(offset<source.length)pull(request,replies,errors);
 }
 check([received isEqual:source]&&replies.count==3,"ordered raw bytes survive exact bounded pulls");
 pull(request,replies,errors);check(replies.count==3,"drained body still awaits genuine upstream EOF");bytes(request,[NSData data],YES,nil);
 check([replies.lastObject isEqual:@{@"done":@YES,@"bodyBase64":@""}]&&!errors.count&&request.completed&&!PorticoQueueRequests[request.identifier],"only exact upstream EOF closes the handle");
 pull(request,replies,errors);check([errors.lastObject isEqual:@"cancelled"],"closed handle never fabricates another EOF");

 request=make();task=install(request);headers(task,403);[replies removeAllObjects];[errors removeAllObjects];
 check(opened==1&&[opening[@"status"]isEqual:@403],"error status is never reinterpreted as success");
 pull(request,replies,errors);bytes(request,[@"denied" dataUsingEncoding:NSUTF8StringEncoding],YES,nil);
 check([replies.lastObject[@"bodyBase64"]isEqual:@"ZGVuaWVk"]&&[replies.lastObject[@"done"]isEqual:@NO],"error body remains original bytes");
 pull(request,replies,errors);check([replies.lastObject[@"done"]isEqual:@YES],"error status still has an exact body end");

 for(NSString *domain in @[@"test_refused_certificate",NSURLErrorDomain,@"synthetic_other_failure"]){
  request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];
  NSError *error=[NSError errorWithDomain:domain code:[domain isEqual:NSURLErrorDomain]?NSURLErrorTimedOut:1 userInfo:nil];
  [request transportFailed:error];
  check(request.completed&&request.streamTerminalFailure&&!request.data&&!request.wire&&!request.task&&!request.streamDriver,"post-open failure retains only bounded terminal metadata");
  pull(request,replies,errors);NSString *expected=[domain isEqual:@"test_refused_certificate"]?@"certificate_untrusted":[domain isEqual:NSURLErrorDomain]?@"timeout":@"queue_transport";
  check(!replies.count&&[errors.lastObject isEqual:expected]&&!PorticoQueueRequests[request.identifier],"next pull sees original failure, never EOF or retry");
 }
 request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];pull(request,replies,errors);
 ReadingStreamDriver *lateDriver=(id)request.streamDriver;void (^late)(NSData *,BOOL,NSError *)=lateDriver.pending;cancel(request);
 check(!replies.count&&[errors.lastObject isEqual:@"cancelled"]&&!PorticoQueueRequests[request.identifier],"pending read rejects exactly on cancellation");
 late([NSData data],YES,nil);[request transportFailed:[NSError errorWithDomain:NSURLErrorDomain code:NSURLErrorTimedOut userInfo:nil]];
 check(!replies.count&&errors.count==1&&[request.failureCode isEqual:@"cancelled"],"late EOF/error cannot replace cancellation");
 request=make();task=install(request);cancel(request);headers(task,200);
 check(openRejected==1&&[openError isEqual:@"cancelled"]&&!opened,"pre-open cancellation rejects once despite late metadata");
 request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];
 PorticoReadingStreamRead(request.identifier,[NSObject new],^(id value){[replies addObject:value];},^(NSString *c,NSString *m,NSError *e){[errors addObject:c];});
 check([errors.lastObject isEqual:@"cancelled"]&&!request.completed&&[(ReadingStreamDriver *)request.streamDriver reads]==0,"another runtime owner cannot consume this request");
 [PorticoQueueTransport cancelOwnedBy:owner];check(request.completed&&!PorticoQueueRequests[request.identifier],"owner invalidation removes the handle");

 for(NSUInteger length=PorticoStreamPullLimit+1;length<=PorticoStreamChunkLimit+1;length=PorticoStreamChunkLimit+1){
  request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];pull(request,replies,errors);
  bytes(request,[NSMutableData dataWithLength:length],NO,nil);
  check(request.completed&&!request.data&&task.cancels==1&&!replies.count&&[errors.lastObject isEqual:@"queue_transport"],"oversized native delivery fails closed without truncation or EOF");
  if(length==PorticoStreamChunkLimit+1)break;
 }
 request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];pull(request,replies,errors);bytes(request,[NSData data],YES,nil);
 check(!replies.count&&[errors.lastObject isEqual:@"queue_transport"],"premature EOF cannot satisfy declared source length");
 request=make();task=install(request);headers(task,200);[replies removeAllObjects];[errors removeAllObjects];
 __block BOOL followed=YES;[transport URLSession:nil task:task willPerformHTTPRedirection:nil newRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:@"https://elsewhere.example"]] completionHandler:^(NSURLRequest *next){followed=next!=nil;}];
 pull(request,replies,errors);check(!followed&&!replies.count&&[errors.lastObject isEqual:@"queue_transport"],"same transport refuses redirects");
 check(PorticoQueueActive==0&&PorticoQueueReads==0&&!PorticoQueueRequests.count,"all admission slots/handles released exactly once");
 for(NSUInteger attempt=0;attempt<10;attempt++)actualPullStream(attempt%2==1);
 for(NSUInteger mode=1;mode<=3;mode++)actualCancel(mode);
 actualResponse(200,@"x",YES,NO);
 for(NSNumber *status in @[@200,@204,@304])actualResponse(status.integerValue,@"",NO,NO);
 actualResponse(403,@"denied",NO,NO);
 actualResponse(200,@"",NO,YES);
 printf("Apple Reading pull stream: %lu checks passed\n",(unsigned long)checks);return 0;
}}
