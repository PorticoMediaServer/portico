#import "../Native/PorticoQueueTransport.h"

// These tests exercise the real request builder/delegate/completion. Trust delegation is
// observed, not replaced with a claim about live TLS or a reader session.
static NSInteger trustCalls;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {trustCalls++;completion(NSURLSessionAuthChallengePerformDefaultHandling,nil);}
+ (BOOL)isRefusal:(NSError *)error {return [error.domain isEqual:@"test_refused_certificate"];}
@end
#pragma clang diagnostic pop

@interface ReadingBytesTask : NSURLSessionDataTask
@property(nonatomic,copy) NSString *testID;
@property(nonatomic) BOOL cancelled;
@end
@implementation ReadingBytesTask
- (NSString *)taskDescription{return _testID;}
- (NSURLSessionTaskState)state{return _cancelled?NSURLSessionTaskStateCanceling:NSURLSessionTaskStateRunning;}
- (void)cancel{_cancelled=YES;}
@end

static NSUInteger checks;
static void check(BOOL ok,const char *message){checks++;if(!ok){fprintf(stderr,"FAIL %s\n",message);exit(1);}}
static PorticoQueueTransport *transport;
static NSURLSession *session;
static NSObject *owner;
static NSDictionary *answer;
static NSString *header(NSString *name){for(NSString *key in answer[@"headers"])if([key caseInsensitiveCompare:name]==NSOrderedSame)return answer[@"headers"][key];return nil;}
static NSString *failure;
static NSUInteger resolved,rejected;
static PorticoQueueRequest *make(NSString *method,NSString *suffix,NSNumber *limit){
 answer=nil;failure=nil;resolved=0;rejected=0;
 return PorticoReadingBytesRequest(NSUUID.UUID.UUIDString,[@"https://server.example/v1/reading/sessions/s-1/" stringByAppendingString:suffix],method,@{@"Range":@"bytes=0-4",@"If-Match":@"\"revision\""},limit,owner,^(id raw){resolved++;answer=raw;},^(NSString *code,NSString *message,NSError *error){rejected++;failure=code;});
}
static ReadingBytesTask *install(PorticoQueueRequest *request){
 ReadingBytesTask *task=[ReadingBytesTask new];task.testID=request.identifier;request.task=task;
 if(!PorticoQueueRequests)PorticoQueueRequests=[NSMutableDictionary new];
 PorticoQueueRequests[request.identifier]=request;return task;
}
static NSURLSessionResponseDisposition respond(ReadingBytesTask *task,NSInteger status,NSDictionary *headers){
 __block NSURLSessionResponseDisposition disposition=-1;
 NSHTTPURLResponse *response=[[NSHTTPURLResponse alloc]initWithURL:[NSURL URLWithString:@"https://server.example"] statusCode:status HTTPVersion:@"HTTP/1.1" headerFields:headers];
 [transport URLSession:session dataTask:task didReceiveResponse:response completionHandler:^(NSURLSessionResponseDisposition value){disposition=value;}];return disposition;
}
int main(void){@autoreleasepool{
 transport=[PorticoQueueTransport new];owner=[NSObject new];session=[PorticoQueueTransport sessionFor:NO];
 for(NSString *path in @[@"source",@"assets?path=OPS%2Fpage.xhtml",@"pages/0?width=1024"]){
  PorticoQueueRequest *r=make(@"GET",path,@33554432);check(r&&r.binaryResponse&&r.responseLimit==33554432,"all three canonical GET resources use explicit bounded raw bytes");
  check(r.wire.timeoutInterval==15&&[r.wire valueForHTTPHeaderField:@"If-Match"]!=nil,"deadline and source precondition retained");
 }
 for(id limit in @[@0,@(-1),@1.5,@33554433,@YES,@(NAN),@"64",NSNull.null])check(!make(@"GET",@"source",limit)&&rejected==1,"invalid/nonintegral/boolean/oversized limits refused");
 for(NSString *path in @[@"",@"progress",@"source/extra",@"%73ource",@"pages/-1",@"pages/01",@"../other/source",@"source#fragment"])
  check(!make(@"GET",path,@64),"noncanonical resource rejected");
 for(NSString *method in @[@"POST",@"PUT",@"DELETE",@"get"])check(!make(method,@"source",@64),"no write or method alias in byte API");
 check(!make(@"HEAD",@"assets?path=x",@64)&&!make(@"HEAD",@"pages/0",@64),"HEAD is source-only");
 for(NSString *url in @[@"https://user:pass@server.example/v1/reading/sessions/s/source",@"file:///v1/reading/sessions/s/source",@"https://server.example/v1/system",@"https://server.example/v1/reading/sessions//source"]){
  rejected=0;PorticoQueueRequest *r=PorticoReadingBytesRequest(@"id",url,@"GET",@{},@64,owner,^(id raw){},^(NSString *c,NSString *m,NSError *e){rejected++;});check(!r&&rejected==1,"credential-free canonical HTTP API URL required");
 }
 PorticoQueueRequest *r=make(@"GET",@"source",@5);ReadingBytesTask *task=install(r);
 check(respond(task,206,@{@"Content-Length":@"5",@"Content-Type":@"application/pdf",@"ETag":@"\"revision\"",@"Content-Range":@"bytes 0-4/100"})==NSURLSessionResponseAllow,"real 206 metadata accepted");
 const unsigned char raw[]={0,255,192,128,127};NSData *bytes=[NSData dataWithBytes:raw length:5];
 [transport URLSession:session dataTask:task didReceiveData:[bytes subdataWithRange:NSMakeRange(0,2)]];
 [transport URLSession:session dataTask:task didReceiveData:[bytes subdataWithRange:NSMakeRange(2,3)]];
 [transport URLSession:session task:task didCompleteWithError:nil];
 check(resolved==1&&!failure&&[answer[@"status"]isEqual:@206],"binary response resolves once with original status");
 check([[[NSData alloc]initWithBase64EncodedString:answer[@"bodyBase64"] options:0]isEqual:bytes]&&!answer[@"body"],"NUL and invalid UTF8 survive exact base64 without text fallback");
 check([header(@"Content-Range")isEqual:@"bytes 0-4/100"]&&[header(@"ETag")isEqual:@"\"revision\""],"Range and revision response facts retained");
 check(r.data==nil&&r.task==nil&&!PorticoQueueRequests[r.identifier],"completion releases binary buffer and request admission");
 [transport URLSession:session task:task didCompleteWithError:nil];check(resolved==1&&rejected==0,"late completion cannot settle again");
 r=make(@"HEAD",@"source",@65536);task=install(r);
 check(respond(task,200,@{@"Content-Length":@"2147483648",@"Content-Type":@"application/pdf"})==NSURLSessionResponseAllow,"HEAD full resource size is not received body length");
 [transport URLSession:session task:task didCompleteWithError:nil];check([answer[@"bodyBase64"]isEqual:@""]&&[header(@"Content-Length")isEqual:@"2147483648"],"HEAD keeps actual total size with empty bytes");
 r=make(@"GET",@"pages/0",@4);task=install(r);check(respond(task,200,@{@"Content-Length":@"5"})==NSURLSessionResponseCancel&&rejected==1&&task.cancelled,"declared oversized body rejected before data");
 r=make(@"GET",@"assets?path=x",@4);task=install(r);respond(task,200,@{});
 [transport URLSession:session dataTask:task didReceiveData:[NSMutableData dataWithLength:3]];
 [transport URLSession:session dataTask:task didReceiveData:[NSMutableData dataWithLength:2]];
 check(rejected==1&&resolved==0&&task.cancelled&&r.data==nil,"streamed overrun cancels and discards bytes");
 r=make(@"GET",@"source",@64);task=install(r);respond(task,412,@{@"Content-Type":@"application/json"});
 NSData *json=[@"{\"error\":\"source_changed\"}" dataUsingEncoding:NSUTF8StringEncoding];
 [transport URLSession:session dataTask:task didReceiveData:json];[transport URLSession:session task:task didCompleteWithError:nil];
 check([answer[@"status"]isEqual:@412]&&[[[NSData alloc]initWithBase64EncodedString:answer[@"bodyBase64"] options:0]isEqual:json],"bounded HTTP error JSON remains real status and raw bytes");
 r=make(@"GET",@"source",@64);task=install(r);__block BOOL redirected=YES;
 [transport URLSession:session task:task willPerformHTTPRedirection:[[NSHTTPURLResponse alloc]initWithURL:r.wire.URL statusCode:302 HTTPVersion:@"HTTP/1.1" headerFields:@{}] newRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:@"https://other.example"]] completionHandler:^(NSURLRequest *next){redirected=next!=nil;}];
 check(!redirected&&rejected==1&&task.cancelled,"same transport refuses redirects");
 r=make(@"GET",@"source",@64);task=install(r);r.failureCode=@"cancelled";[r finish:@"cancelled"];
 [transport URLSession:session task:task didCompleteWithError:nil];check([failure isEqual:@"cancelled"]&&rejected==1&&resolved==0,"existing cancel code settles once; stale data cannot revive it");
 r=make(@"GET",@"source",@64);task=install(r);[PorticoQueueTransport cancelOwnedBy:owner];check(rejected==1&&task.cancelled,"owner invalidation cancels binary requests too");
 [transport URLSession:session didReceiveChallenge:[NSURLAuthenticationChallenge new] completionHandler:^(NSURLSessionAuthChallengeDisposition d,NSURLCredential *c){check(d==NSURLSessionAuthChallengePerformDefaultHandling,"trust delegate result preserved");}];check(trustCalls==1,"binary and text sessions share PorticoServerTrust delegate");
 r=make(@"GET",@"source",@64);task=install(r);[transport URLSession:session task:task didCompleteWithError:[NSError errorWithDomain:@"test_refused_certificate" code:1 userInfo:nil]];check([failure isEqual:@"certificate_untrusted"],"refused leaf retains existing identity-gate rejection code");
 NSURLSessionConfiguration *configuration=[PorticoQueueTransport sessionFor:NO].configuration;
 check(!configuration.HTTPShouldSetCookies&&!configuration.HTTPCookieStorage&&!configuration.URLCredentialStorage&&!configuration.URLCache&&configuration.timeoutIntervalForResource==15,"same ephemeral pool has no cookie credential or cache persistence");
 r=make(@"GET",@"source",@64);r.binaryResponse=NO;task=install(r);respond(task,200,@{});[transport URLSession:session dataTask:task didReceiveData:json];[transport URLSession:session task:task didCompleteWithError:nil];check([answer[@"body"]isEqual:[[NSString alloc]initWithData:json encoding:NSUTF8StringEncoding]]&&!answer[@"bodyBase64"],"existing JSON text completion remains unchanged");
 NSMutableURLRequest *preview=[NSMutableURLRequest requestWithURL:[NSURL URLWithString:@"https://server.test/v1/metadata/bulk/preview"]];preview.HTTPMethod=@"POST";
 check(PorticoBulkMetadataPreview(preview),"exact owner preview gets caller-owned lifetime");
 preview.HTTPMethod=@"GET";check(!PorticoBulkMetadataPreview(preview),"ordinary reads keep their deadline");
 preview.HTTPMethod=@"POST";preview.URL=[NSURL URLWithString:@"https://server.test/v1/metadata/bulk/preview?extra=1"];check(!PorticoBulkMetadataPreview(preview),"malformed preview cannot bypass ordinary deadlines");
 NSURLSessionConfiguration *previewConfiguration=[PorticoQueueTransport sessionFor:NO subtitleUpload:NO bulkPreview:YES].configuration;
 check(previewConfiguration.timeoutIntervalForResource==NSURLSessionConfiguration.ephemeralSessionConfiguration.timeoutIntervalForResource&&previewConfiguration.timeoutIntervalForRequest==previewConfiguration.timeoutIntervalForResource,"preview keeps Foundation resource policy instead of the ordinary fifteen-second cap");
 check(!previewConfiguration.HTTPShouldSetCookies&&!previewConfiguration.HTTPCookieStorage&&!previewConfiguration.URLCredentialStorage&&!previewConfiguration.URLCache,"preview keeps private transport configuration");
 r=make(@"GET",@"source",@64);r.bulkPreview=YES;task=install(r);[PorticoQueueTransport cancelOwnedBy:owner];check(rejected==1&&task.cancelled,"caller cancellation still releases a preview read");
 [[PorticoQueueTransport sessionFor:NO subtitleUpload:NO bulkPreview:YES] invalidateAndCancel];
 [[PorticoQueueTransport sessionFor:NO] invalidateAndCancel];[[PorticoQueueTransport sessionFor:YES] invalidateAndCancel];
 printf("Apple Reading byte transport: %lu checks passed\n",(unsigned long)checks);
 return 0;
}}
