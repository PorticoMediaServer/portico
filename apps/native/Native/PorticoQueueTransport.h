#import <Foundation/Foundation.h>
#import <math.h>
#import "PorticoServerTrust.h"
#import "PorticoReadingStreamDriver.h"

typedef void (^PorticoTransportResolve)(id result);
typedef void (^PorticoTransportReject)(NSString *code,NSString *message,NSError *error);

// Queue HTTP requests carry a device proof. Never follow redirects, bypass TLS
// validation, or accumulate an unbounded response. There is no background media
// player here; Reading also uses this same transport for bounded binary reads.
//
// PERF-04: every request used to open its own NSURLSession, so each API call paid a new TCP
// connection and TLS handshake, and a 17th concurrent request was rejected outright. Now four
// shared, ephemeral, cookie-less, cache-less sessions (so connections and TLS sessions are
// reused), at most 12 requests in flight and up to 64 waiting, with control work first. The security bounds are
// unchanged and enforced per task: no redirects, 1 MB controls / 8 MB API GETs / up to 32 MB Reading bytes.
// Ordinary sessions use 15 s; subtitle uploads use 60 s idle / 110 s total.
// Paged owner metadata previews use caller cancellation and Foundation resource policy.
@interface PorticoQueueRequest : NSObject
@property(nonatomic,strong) NSURLSessionTask *task;
@property(nonatomic,strong) NSURLRequest *wire;
@property(nonatomic,strong) NSMutableData *data;
@property(nonatomic,strong) NSHTTPURLResponse *response;
@property(nonatomic,copy) PorticoTransportResolve resolve;
@property(nonatomic,copy) PorticoTransportReject reject;
@property(nonatomic,copy) NSString *identifier;
@property(nonatomic,weak) id owner;
@property(nonatomic) BOOL completed;
@property(nonatomic) BOOL started;
@property(nonatomic) BOOL critical;
@property(nonatomic) BOOL subtitleUpload;
@property(nonatomic) BOOL bulkPreview;
@property(nonatomic) NSUInteger responseLimit;
@property(nonatomic) BOOL binaryResponse;
@property(nonatomic) BOOL streamingResponse;
@property(nonatomic) BOOL streamOpened;
@property(nonatomic) BOOL streamEOF;
@property(nonatomic) BOOL streamRetired;
@property(nonatomic,strong) id<PorticoReadingStreamDriver> streamDriver;
@property(nonatomic) BOOL streamPullInFlight;
@property(nonatomic) uint64_t streamByteCount;
@property(nonatomic,copy) PorticoTransportResolve streamResolve;
@property(nonatomic,copy) PorticoTransportReject streamReject;
@property(nonatomic,copy) NSString *streamTerminalFailure;
/** Set before -finish: when the failure was a refused certificate. */
@property(nonatomic,copy) NSString *failureCode;
- (void)finish:(NSString *)failure;
- (void)transportFailed:(NSError *)error;
- (void)openStreamResponse:(NSHTTPURLResponse *)response;
- (void)deliverStream;
- (void)readStreamOwnedBy:(id)owner resolve:(PorticoTransportResolve)resolve reject:(PorticoTransportReject)reject;
@end
static NSMutableDictionary<NSString *,PorticoQueueRequest *> *PorticoQueueRequests;
static NSMutableArray<PorticoQueueRequest *> *PorticoQueueWaiting;
static NSInteger PorticoQueueActive, PorticoQueueReads;
static const NSInteger PorticoQueueMaxActive=12;
static const NSTimeInterval PorticoSubtitleUploadIdleTimeout=60,PorticoSubtitleUploadTimeout=110;
static const NSUInteger PorticoStreamChunkLimit=1048576,PorticoStreamPullLimit=49152;

static BOOL PorticoBulkMetadataPreview(NSURLRequest *wire) {
 return [wire.HTTPMethod isEqualToString:@"POST"]&&[wire.URL.path isEqualToString:@"/v1/metadata/bulk/preview"]&&wire.URL.query==nil;
}

static NSDictionary *PorticoQueueResponseHeaders(NSHTTPURLResponse *response) {
 NSMutableDictionary *headers=[NSMutableDictionary dictionary];
 for(id key in response.allHeaderFields)headers[[key description]]=[response.allHeaderFields[key] description];
 return headers;
}

@interface PorticoQueueTransport : NSObject<NSURLSessionDataDelegate,NSURLSessionTaskDelegate> @end
@implementation PorticoQueueTransport
+ (NSURLSession *)sessionFor:(BOOL)critical subtitleUpload:(BOOL)subtitleUpload bulkPreview:(BOOL)bulkPreview {
 static NSURLSession *reads,*writes,*uploads,*previews;static dispatch_once_t once;
 dispatch_once(&once,^{
  // Separate socket pools keep ten slow reads from occupying the control connections.
  // Resource timeouts apply to every task in a session, so uploads need a separate lane.
  for(NSInteger lane=0;lane<4;lane++){
   NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;
   // A paged preview keeps Foundation's default resource lifetime; caller
   // cancellation owns the operation. Other lanes keep their existing timers.
   if(lane==3)configuration.timeoutIntervalForRequest=configuration.timeoutIntervalForResource;
   else {configuration.timeoutIntervalForRequest=lane==2?PorticoSubtitleUploadIdleTimeout:15;configuration.timeoutIntervalForResource=lane==2?PorticoSubtitleUploadTimeout:15;}configuration.HTTPShouldSetCookies=NO;configuration.HTTPCookieStorage=nil;configuration.URLCredentialStorage=nil;configuration.URLCache=nil;configuration.requestCachePolicy=NSURLRequestReloadIgnoringLocalCacheData;configuration.HTTPMaximumConnectionsPerHost=lane?2:10;
   NSOperationQueue *queue=[NSOperationQueue new];queue.maxConcurrentOperationCount=1;queue.name=lane==3?@"tv.getportico.metadata-preview":lane==2?@"tv.getportico.subtitle-uploads":lane?@"tv.getportico.control":@"tv.getportico.reads";
   NSURLSession *session=[NSURLSession sessionWithConfiguration:configuration delegate:[PorticoQueueTransport new] delegateQueue:queue];
   if(lane==3)previews=session;else if(lane==2)uploads=session;else if(lane)writes=session;else reads=session;
  }
 });
 return bulkPreview?previews:subtitleUpload?uploads:critical?writes:reads;
}
+ (NSURLSession *)sessionFor:(BOOL)critical subtitleUpload:(BOOL)subtitleUpload {
 return [self sessionFor:critical subtitleUpload:subtitleUpload bulkPreview:NO];
}
+ (NSURLSession *)sessionFor:(BOOL)critical {
 return [self sessionFor:critical subtitleUpload:NO];
}
/** Caller holds the class lock. */
+ (void)start:(PorticoQueueRequest *)request {
 request.started=YES;PorticoQueueActive++;if(!request.critical)PorticoQueueReads++;
 if(request.streamingResponse){
  Class driverClass=NSClassFromString(@"PorticoReadingByteStream");
  if(!driverClass){[request finish:@"network_stream_unavailable"];return;}
  request.streamDriver=[driverClass new];
  NSMutableURLRequest *wire=[request.wire mutableCopy];[NSURLProtocol setProperty:request.identifier forKey:@"portico.queue.stream" inRequest:wire];request.wire=wire;
  __weak PorticoQueueRequest *weakRequest=request;
  [request.streamDriver openWithSession:[self sessionFor:request.critical] request:wire attached:^(NSURLSessionTask *task){
   PorticoQueueRequest *current=weakRequest;if(!current){[task cancel];return;}
   @synchronized(current){if(current.completed){[task cancel];return;}task.taskDescription=current.identifier;current.task=task;}
  } headers:^(NSHTTPURLResponse *response){
   PorticoQueueRequest *current=weakRequest;if(!current)return;
   @synchronized(current){
    [current openStreamResponse:response];
   }
  } failure:^(NSError *error){PorticoQueueRequest *current=weakRequest;if(current)@synchronized(current){if(!current.completed)[current transportFailed:error];}}];
  return;
 }
 NSURLSessionDataTask *task=[[self sessionFor:request.critical subtitleUpload:request.subtitleUpload bulkPreview:request.bulkPreview] dataTaskWithRequest:request.wire];request.task=task;
 task.taskDescription=request.identifier;
 [task resume];
}
/** Caller holds the class lock. Two slots and eight waiting places are reserved for writes. */
+ (void)pump {
 while(PorticoQueueActive<PorticoQueueMaxActive&&PorticoQueueWaiting.count){
  NSUInteger index=[PorticoQueueWaiting indexOfObjectPassingTest:^BOOL(PorticoQueueRequest *r,NSUInteger i,BOOL *stop){return r.critical;}];
  if(index==NSNotFound){if(PorticoQueueReads>=10)break;index=0;}
  PorticoQueueRequest *next=PorticoQueueWaiting[index];[PorticoQueueWaiting removeObjectAtIndex:index];[self start:next];
 }
}
+ (void)submit:(PorticoQueueRequest *)request domain:(NSString *)domain busy:(NSString *)busy {
 @synchronized(PorticoQueueRequest.class){
  if(!PorticoQueueRequests)PorticoQueueRequests=[NSMutableDictionary dictionary];if(!PorticoQueueWaiting)PorticoQueueWaiting=[NSMutableArray array];
  request.bulkPreview=PorticoBulkMetadataPreview(request.wire);
  request.critical=!request.bulkPreview&&![@[@"GET",@"HEAD"] containsObject:request.wire.HTTPMethod];
  if(request.bulkPreview){NSMutableURLRequest *wire=[request.wire mutableCopy];wire.timeoutInterval=[self sessionFor:NO subtitleUpload:NO bulkPreview:YES].configuration.timeoutIntervalForRequest;request.wire=wire;}
  NSUInteger reads=0;for(PorticoQueueRequest *r in PorticoQueueRequests.allValues)if(!r.critical)reads++;
  if(PorticoQueueRequests[request.identifier]||PorticoQueueRequests.count>=76||(!request.critical&&reads>=66)){request.reject(domain,busy,nil);return;}
  PorticoQueueRequests[request.identifier]=request;[PorticoQueueWaiting addObject:request];[self pump];
  // Include queue residence in the operation's deadline. Cancellation releases admission once.
  NSTimeInterval timeout=request.subtitleUpload?PorticoSubtitleUploadTimeout:request.wire.timeoutInterval;
  if(!request.bulkPreview)dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(timeout*NSEC_PER_SEC)),dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{@synchronized(request){if(!request.completed){request.failureCode=@"timeout";[request finish:@"The request took too long."];}}});
 }
}
/** Caller holds the class lock. */
+ (void)release:(PorticoQueueRequest *)request {
 if(!request.streamTerminalFailure&&PorticoQueueRequests[request.identifier]==request)[PorticoQueueRequests removeObjectForKey:request.identifier];
 if(!request.started){[PorticoQueueWaiting removeObjectIdenticalTo:request];return;}
 request.started=NO;
 PorticoQueueActive=MAX(0,PorticoQueueActive-1);if(!request.critical)PorticoQueueReads=MAX(0,PorticoQueueReads-1);
 [self pump];
}
+ (void)cancelOwnedBy:(id)owner {
 NSArray<PorticoQueueRequest *> *owned;
 @synchronized(PorticoQueueRequest.class){owned=[PorticoQueueRequests.allValues filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(PorticoQueueRequest *r,NSDictionary *b){return r.owner==owner||r.owner==nil;}]];}
 for(PorticoQueueRequest *request in owned){
  @synchronized(request){
   request.streamRetired=YES;request.failureCode=@"cancelled";request.streamTerminalFailure=nil;
   if(request.completed){@synchronized(PorticoQueueRequest.class){[self release:request];}continue;}
   [request finish:@"The app reloaded."];
  }
 }
}
- (PorticoQueueRequest *)requestFor:(NSURLSessionTask *)task {@synchronized(PorticoQueueRequest.class){
 PorticoQueueRequest *request=task.taskDescription?PorticoQueueRequests[task.taskDescription]:nil;
 if(!request){
  id identifier=task.originalRequest?[NSURLProtocol propertyForKey:@"portico.queue.stream" inRequest:task.originalRequest]:nil;
  request=[identifier isKindOfClass:NSString.class]?PorticoQueueRequests[identifier]:nil;
  if(request.streamDriver&&!request.completed&&!request.task&&[request.wire.URL isEqual:task.originalRequest.URL]){request.task=task;task.taskDescription=request.identifier;}
 }
 return request.task==task?request:nil;
}}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler {
 completionHandler(nil);[[self requestFor:task] finish:@"Queue control redirects are not allowed."];
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)response completionHandler:(void (^)(NSURLSessionResponseDisposition))completionHandler {
 PorticoQueueRequest *request=[self requestFor:task];
 // HEAD describes the complete resource, not bytes transferred in this response.
 BOOL head=[request.wire.HTTPMethod isEqualToString:@"HEAD"];
 if(!request||![response isKindOfClass:NSHTTPURLResponse.class]||(!request.streamingResponse&&!head&&response.expectedContentLength>(long long)(request.responseLimit?:1048576))){completionHandler(NSURLSessionResponseCancel);[request finish:@"Queue response exceeded its limit."];return;}
 @synchronized(request){
  if(request.completed){completionHandler(NSURLSessionResponseCancel);return;}
  request.response=(NSHTTPURLResponse *)response;
  if(request.streamingResponse){
   if(request.streamDriver)completionHandler(NSURLSessionResponseAllow);
   else{completionHandler(NSURLSessionResponseCancel);[request finish:@"network_stream_driver_missing"];}
   return;
  }
  completionHandler(NSURLSessionResponseAllow);
 }
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
 PorticoQueueRequest *request=[self requestFor:task];if(!request)return;
 BOOL over=NO;@synchronized(request){
  if(request.completed)return;
  if(request.streamingResponse){
   // There is no suspended-data-task fallback: only AsyncBytes supplies the body.
   if(!request.streamDriver)[request finish:@"network_stream_driver_missing"];
   return;
  }else{
   NSUInteger limit=request.responseLimit?:1048576;if(request.data.length>limit||data.length>limit-request.data.length)over=YES;else [request.data appendData:data];
  }
 }
 if(over)[request finish:@"Queue response exceeded its limit."];
}
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
 [PorticoServerTrust handleChallenge:challenge completion:completionHandler];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 PorticoQueueRequest *request=[self requestFor:task];
 if(request.streamDriver){if(error)[request transportFailed:error];return;}
 [request transportFailed:error];
}
@end

@implementation PorticoQueueRequest
- (void)openStreamResponse:(NSHTTPURLResponse *)response {
 @synchronized(self){
  if(self.completed||self.streamOpened)return;
  if(![response.URL isEqual:self.wire.URL]){[self finish:@"network_stream_response_invalid"];return;}
  self.response=response;self.streamOpened=YES;
  PorticoTransportResolve resolve=self.resolve;self.resolve=nil;self.reject=nil;
  resolve(@{@"status":@(response.statusCode),@"headers":PorticoQueueResponseHeaders(response)});
 }
}
- (void)transportFailed:(NSError *)error {
 @synchronized(self){if(self.completed)return;
 PorticoQueueRequest *request=self;
 if([PorticoServerTrust isRefusal:error]){request.failureCode=@"certificate_untrusted";[request finish:@"The server's certificate is not trusted yet."];return;}
 // What the failure means to the person, as Android reports it: no answer in time, or nothing reached
 // the server. Anything else keeps the transport's own code (a write's outcome stays unknown).
 if([error.domain isEqualToString:NSURLErrorDomain])switch(error.code){
  case NSURLErrorTimedOut:request.failureCode=@"timeout";[request finish:@"The request took too long."];return;
  case NSURLErrorCannotFindHost:case NSURLErrorCannotConnectToHost:case NSURLErrorDNSLookupFailed:case NSURLErrorNotConnectedToInternet:
   request.failureCode=@"network";[request finish:@"The server could not be reached."];return;
  default:break;
 }
 [request finish:error?@"The queue request could not be confirmed.":nil];
 }
}
/** Caller holds the request lock. Only a pending pull may resume the upstream task. */
- (void)deliverStream {
 if(!self.streamResolve||self.completed)return;
 if(self.data.length){
  NSUInteger length=MIN(self.data.length,PorticoStreamPullLimit);
  NSString *body=[[self.data subdataWithRange:NSMakeRange(0,length)] base64EncodedStringWithOptions:0];
  [self.data replaceBytesInRange:NSMakeRange(0,length) withBytes:NULL length:0];
  PorticoTransportResolve resolve=self.streamResolve;self.streamResolve=nil;self.streamReject=nil;
  resolve(@{@"done":@NO,@"bodyBase64":body});return;
 }
 if(self.streamEOF){
  PorticoTransportResolve resolve=self.streamResolve;self.streamResolve=nil;self.streamReject=nil;
  [self finish:nil];resolve(@{@"done":@YES,@"bodyBase64":@""});return;
 }
 if(self.streamDriver){
  if(self.streamPullInFlight)return;
  self.streamPullInFlight=YES;id<PorticoReadingStreamDriver> driver=self.streamDriver;
  [driver readWithCompletion:^(NSData *data,BOOL atEOF,NSError *error){
   @synchronized(self){
    if(self.completed||self.streamDriver!=driver)return;self.streamPullInFlight=NO;
    if(error){[self transportFailed:error];return;}
    if(data.length>PorticoStreamPullLimit||(!data.length&&!atEOF)||self.streamByteCount>UINT64_MAX-data.length){[self finish:@"network_stream_read_invalid"];return;}
    self.streamByteCount+=data.length;
    // Content-Length describes encoded bytes; compare only an identity body.
    NSString *encoding=nil;for(id key in self.response.allHeaderFields)if([[key description] caseInsensitiveCompare:@"Content-Encoding"]==NSOrderedSame)encoding=[self.response.allHeaderFields[key] description];
    long long expected=([@[@204,@304] containsObject:@(self.response.statusCode)])?0:self.response.expectedContentLength;
    if(atEOF&&(!encoding.length||[encoding caseInsensitiveCompare:@"identity"]==NSOrderedSame)&&expected>=0&&self.streamByteCount!=(uint64_t)expected){[self finish:@"network_stream_incomplete"];return;}
    [self.data appendData:data];self.streamEOF=atEOF;[self deliverStream];
   }
  }];
  return;
 }
 [self finish:@"network_stream_driver_missing"];
}
- (void)readStreamOwnedBy:(id)owner resolve:(PorticoTransportResolve)resolve reject:(PorticoTransportReject)reject {
 @synchronized(self){
  if(!self.streamingResponse||!self.streamOpened||!owner||self.owner!=owner||self.streamRetired){reject(@"cancelled",@"network_stream_retired",nil);return;}
  if(self.completed){
   NSString *code=self.failureCode?:@"queue_transport",*failure=self.streamTerminalFailure?:@"network_stream_retired";
   self.streamTerminalFailure=nil;@synchronized(PorticoQueueRequest.class){[PorticoQueueTransport release:self];}
   reject(code,failure,nil);return;
  }
  if(self.streamResolve){reject(@"queue_transport",@"network_stream_read_pending",nil);return;}
  self.streamResolve=resolve;self.streamReject=reject;[self deliverStream];
 }
}
- (void)finish:(NSString *)failure {
 @synchronized(self) {if(self.completed)return;self.completed=YES;
 [self.streamDriver cancel];
 if(self.task.state==NSURLSessionTaskStateRunning||self.task.state==NSURLSessionTaskStateSuspended)[self.task cancel];
 if(self.streamingResponse&&self.streamOpened&&failure&&!self.streamReject&&self.owner&&!self.streamRetired)self.streamTerminalFailure=failure;
 @synchronized(PorticoQueueRequest.class){[PorticoQueueTransport release:self];}
 if(self.streamingResponse&&self.streamOpened){
  if(failure&&self.streamReject)self.streamReject(self.failureCode?:@"queue_transport",failure,nil);
  self.streamResolve=nil;self.streamReject=nil;
  // Retain only a bounded terminal error (no URL/body/credentials) for the next pull.
  // An abandoned handle expires instead of retaining queue admission indefinitely.
  if(self.streamTerminalFailure)dispatch_after(dispatch_time(DISPATCH_TIME_NOW,15*NSEC_PER_SEC),dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
   @synchronized(self){if(self.streamTerminalFailure){self.streamTerminalFailure=nil;@synchronized(PorticoQueueRequest.class){[PorticoQueueTransport release:self];}}}
  });
 }else if(failure){self.reject(self.failureCode?:@"queue_transport",failure,nil);}else{
  NSString *body=self.binaryResponse?[self.data base64EncodedStringWithOptions:0]:[[NSString alloc] initWithData:self.data encoding:NSUTF8StringEncoding];
  if(!body||!self.response){self.reject(@"queue_transport",@"Invalid queue response.",nil);}else{
   self.resolve(@{@"status":@(self.response.statusCode),@"headers":PorticoQueueResponseHeaders(self.response),self.binaryResponse?@"bodyBase64":@"body":body});
  }
 }
 self.resolve=nil;self.reject=nil;self.task=nil;self.streamDriver=nil;self.wire=nil;self.data=nil;self.response=nil;
 }
}
@end

/** Reading byte reads use the existing queue/trust/session/cancel machinery. This builder
 * grants no authority: the normal identity/certificate gate and server session still apply. */
static PorticoQueueRequest *PorticoReadingBytesRequest(id identifier,id rawURL,id method,id headers,id byteLimit,id owner,PorticoTransportResolve resolve,PorticoTransportReject reject) {
 BOOL valid=[identifier isKindOfClass:NSString.class]&&[identifier length]>0&&[identifier length]<=128&&[rawURL isKindOfClass:NSString.class]&&[rawURL length]>0&&[rawURL length]<=8192&&[method isKindOfClass:NSString.class]&&[@[@"GET",@"HEAD"] containsObject:method]&&[headers isKindOfClass:NSDictionary.class]&&[byteLimit isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)byteLimit)!=CFBooleanGetTypeID();
 double limit=valid?[byteLimit doubleValue]:0;
 valid=valid&&isfinite(limit)&&floor(limit)==limit&&limit>=1&&limit<=33554432;
 NSURLComponents *parts=valid?[NSURLComponents componentsWithString:rawURL]:nil;
 NSString *path=parts.percentEncodedPath;
 BOOL source=path&&[path rangeOfString:@"^/v1/reading/sessions/[A-Za-z0-9_-]{1,128}/source$" options:NSRegularExpressionSearch].location!=NSNotFound;
 BOOL asset=path&&[path rangeOfString:@"^/v1/reading/sessions/[A-Za-z0-9_-]{1,128}/(?:assets|pages/(?:0|[1-9][0-9]*))$" options:NSRegularExpressionSearch].location!=NSNotFound;
 valid=valid&&parts.URL&&parts.host.length&&[@[@"https",@"http"] containsObject:parts.scheme]&&!parts.user&&!parts.password&&!parts.fragment&&(source||([method isEqualToString:@"GET"]&&asset));
 if(valid)for(id name in headers)if(![name isKindOfClass:NSString.class]||![headers[name] isKindOfClass:NSString.class]){valid=NO;break;}
 if(!valid){reject(@"queue_transport",@"network_bytes_request_invalid",nil);return nil;}
 PorticoQueueRequest *request=[PorticoQueueRequest new];request.identifier=identifier;request.resolve=resolve;request.reject=reject;request.data=[NSMutableData data];request.owner=owner;request.binaryResponse=YES;request.responseLimit=(NSUInteger)limit;
 NSMutableURLRequest *wire=[NSMutableURLRequest requestWithURL:parts.URL cachePolicy:NSURLRequestReloadIgnoringLocalCacheData timeoutInterval:15];wire.HTTPMethod=method;
 for(NSString *name in headers)[wire setValue:headers[name] forHTTPHeaderField:name];
 request.wire=wire;return request;
}

/** Only the existing Reading source GET may become a pull stream. */
static PorticoQueueRequest *PorticoReadingStreamRequest(id identifier,id rawURL,id headers,id owner,PorticoTransportResolve resolve,PorticoTransportReject reject) {
 NSURLComponents *parts=[rawURL isKindOfClass:NSString.class]?[NSURLComponents componentsWithString:rawURL]:nil;
 BOOL valid=parts&&!parts.query&&[parts.percentEncodedPath rangeOfString:@"^/v1/reading/sessions/[A-Za-z0-9_-]{1,128}/source$" options:NSRegularExpressionSearch].location!=NSNotFound&&[headers isKindOfClass:NSDictionary.class];
 if(valid)for(id name in headers)if(![name isKindOfClass:NSString.class]||[name caseInsensitiveCompare:@"Range"]==NSOrderedSame){valid=NO;break;}
 if(!valid){reject(@"queue_transport",@"network_stream_request_invalid",nil);return nil;}
 PorticoQueueRequest *request=PorticoReadingBytesRequest(identifier,rawURL,@"GET",headers,@(PorticoStreamChunkLimit),owner,resolve,reject);
 request.streamingResponse=YES;return request;
}

static void PorticoReadingStreamRead(id identifier,id owner,PorticoTransportResolve resolve,PorticoTransportReject reject) {
 PorticoQueueRequest *request;
 @synchronized(PorticoQueueRequest.class){request=[identifier isKindOfClass:NSString.class]?PorticoQueueRequests[identifier]:nil;}
 if(!request){reject(@"cancelled",@"network_stream_retired",nil);return;}
 [request readStreamOwnedBy:owner resolve:resolve reject:reject];
}
