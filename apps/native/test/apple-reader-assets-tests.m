#import <Foundation/Foundation.h>
#import "../Native/PorticoReaderAssetOrigin.h"
#include <fcntl.h>
#include <unistd.h>

static int checks,failures;
static void check(BOOL value,const char *name){checks++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",name);}}
static NSURL *url(NSString *path){return [NSURL URLWithString:[@"portico-reader://bundle/" stringByAppendingString:path]];}
static void spin(BOOL(^done)(void)){NSDate *until=[NSDate dateWithTimeIntervalSinceNow:2];while(!done()&&until.timeIntervalSinceNow>0)[NSRunLoop.mainRunLoop runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:.01]];}

@interface ReaderTestTask : NSObject <WKURLSchemeTask>
@property(nonatomic,strong) NSURLRequest *request;
@property(nonatomic,strong) NSURLResponse *response;
@property(nonatomic,strong) NSMutableData *bytes;
@property(nonatomic,strong) NSError *error;
@property(nonatomic) BOOL finished;
@property(nonatomic,copy) void(^onResponse)(void);
@end
@implementation ReaderTestTask
- (instancetype)init{if((self=[super init]))_bytes=[NSMutableData new];return self;}
- (void)didReceiveResponse:(NSURLResponse *)response{_response=response;if(_onResponse)_onResponse();}
- (void)didReceiveData:(NSData *)data{[_bytes appendData:data];}
- (void)didFinish{_finished=YES;}
- (void)didFailWithError:(NSError *)error{_error=error;}
@end

int main(void){@autoreleasepool{
 NSFileManager *fm=NSFileManager.defaultManager;NSURL *scratch=[NSURL fileURLWithPath:[NSTemporaryDirectory() stringByAppendingPathComponent:[@"reader-assets-" stringByAppendingString:NSUUID.UUID.UUIDString]] isDirectory:YES];
 NSURL *root=[scratch URLByAppendingPathComponent:@"PorticoReader.bundle" isDirectory:YES];
 [fm createDirectoryAtURL:[root URLByAppendingPathComponent:@"assets"] withIntermediateDirectories:YES attributes:nil error:NULL];
 NSData *html=[@"<!doctype html><title>Test</title>" dataUsingEncoding:NSUTF8StringEncoding];
 [html writeToURL:[root URLByAppendingPathComponent:@"index.html"] atomically:YES];
 NSData *module=[@"export const value = 42;" dataUsingEncoding:NSUTF8StringEncoding];
 [module writeToURL:[root URLByAppendingPathComponent:@"assets/worker.mjs"] atomically:YES];
 NSString *mime=nil;NSError *error=nil;
 check([PorticoReaderReadAsset(root,url(@"index.html"),&mime,&error) isEqual:html]&&[mime isEqual:@"text/html"]&&!error,"real bundled entry bytes and MIME");
 check([PorticoReaderReadAsset(root,url(@"assets/worker.mjs"),&mime,NULL) isEqual:module]&&[mime isEqual:@"text/javascript"],"real relative module/worker bytes and MIME");
 for(NSString *bad in @[@"https://bundle/index.html",@"file:///index.html",@"portico-reader://other/index.html",@"portico-reader://user@bundle/index.html",@"portico-reader://bundle:80/index.html",@"portico-reader://bundle/index.html?path=x",@"portico-reader://bundle/index.html#x",@"portico-reader://bundle/../outside.html",@"portico-reader://bundle/%2e%2e/outside.html",@"portico-reader://bundle/assets%2fworker.mjs",@"portico-reader://bundle/assets//worker.mjs",@"portico-reader://bundle/.hidden.js",@"portico-reader://bundle/assets/worker.exe",@"portico-reader://bundle/missing.js",@"portico-reader://bundle/"]){
  error=nil;check(!PorticoReaderReadAsset(root,[NSURL URLWithString:bad],NULL,&error)&&[error.domain isEqual:@"PorticoReaderAssets"],"noncanonical, escaping, unknown or absent resource refused");
 }
 NSURL *outside=[scratch URLByAppendingPathComponent:@"outside.js"];[module writeToURL:outside atomically:YES];
 [fm createSymbolicLinkAtURL:[root URLByAppendingPathComponent:@"linked.js"] withDestinationURL:outside error:NULL];
 check(!PorticoReaderReadAsset(root,url(@"linked.js"),NULL,NULL),"final symlink cannot escape bundle");
 [fm createSymbolicLinkAtURL:[root URLByAppendingPathComponent:@"linkdir"] withDestinationURL:scratch error:NULL];
 check(!PorticoReaderReadAsset(root,url(@"linkdir/outside.js"),NULL,NULL),"intermediate symlink cannot escape bundle");
 NSURL *rootLink=[scratch URLByAppendingPathComponent:@"root-link"];[fm createSymbolicLinkAtURL:rootLink withDestinationURL:root error:NULL];
 check(!PorticoReaderReadAsset(rootLink,url(@"index.html"),NULL,NULL),"bundle root itself cannot be a symlink");
 [fm createDirectoryAtURL:[root URLByAppendingPathComponent:@"directory.js"] withIntermediateDirectories:NO attributes:nil error:NULL];
 check(!PorticoReaderReadAsset(root,url(@"directory.js"),NULL,NULL),"directory is not a regular asset");
 NSURL *large=[root URLByAppendingPathComponent:@"large.pdf"];int fd=open(large.fileSystemRepresentation,O_WRONLY|O_CREAT|O_TRUNC,0600);check(fd>=0&&ftruncate(fd,64LL*1024*1024+1)==0,"bounded sparse oversize fixture created");if(fd>=0)close(fd);
 check(!PorticoReaderReadAsset(root,url(@"large.pdf"),NULL,NULL),"oversize file is refused before allocation");

 PorticoReaderAssetOrigin *handler=[[PorticoReaderAssetOrigin alloc] initWithRoot:root];
 ReaderTestTask *task=[ReaderTestTask new];task.request=[NSURLRequest requestWithURL:url(@"assets/worker.mjs")];
 [handler webView:nil startURLSchemeTask:task];spin(^BOOL{return task.finished||task.error!=nil;});
 check(task.finished&&!task.error&&[task.bytes isEqual:module],"scheme lifecycle delivers real data then finishes");
 NSDictionary *headers=[(NSHTTPURLResponse *)task.response allHeaderFields];
 check([headers[@"X-Content-Type-Options"] isEqual:@"nosniff"]&&[headers[@"Cache-Control"] isEqual:@"no-store"],"native origin response prevents sniffing and stale cache");
 check([headers[@"Content-Security-Policy"] containsString:@"default-src 'none'"]&&[headers[@"Content-Security-Policy"] containsString:@"worker-src 'self' blob:"],"local document has explicit script/worker isolation");
 check(!headers[@"Access-Control-Allow-Credentials"]&&!headers[@"Set-Cookie"],"public bundle asset handler adds no credentials or cookies");
 [@"<!doctype html><script>window.testOnly=1;</script>" writeToURL:[root URLByAppendingPathComponent:@"inline.html"] atomically:YES encoding:NSUTF8StringEncoding error:NULL];
 ReaderTestTask *inlineTask=[ReaderTestTask new];inlineTask.request=[NSURLRequest requestWithURL:url(@"inline.html")];[handler webView:nil startURLSchemeTask:inlineTask];spin(^BOOL{return inlineTask.finished||inlineTask.error!=nil;});
 NSString *inlinePolicy=[(NSHTTPURLResponse *)inlineTask.response allHeaderFields][@"Content-Security-Policy"];
 check([inlinePolicy containsString:@"script-src 'self' blob: 'sha256-jIIuh1oiMj0EHN7rRnhC+sBs8oTMNs+4BdCenQIO2zo='"]&&![[inlinePolicy componentsSeparatedByString:@";"][1] containsString:@"unsafe-inline"],"only exact immutable HTML bootstrap hash permits inline script");
 ReaderTestTask *head=[ReaderTestTask new];NSMutableURLRequest *headRequest=[NSMutableURLRequest requestWithURL:url(@"index.html")];headRequest.HTTPMethod=@"HEAD";head.request=headRequest;
 [handler webView:nil startURLSchemeTask:head];spin(^BOOL{return head.finished||head.error!=nil;});
 check(head.finished&&head.bytes.length==0&&head.response.expectedContentLength==html.length,"HEAD reports real length without body");
 for(NSString *header in @[@"Authorization",@"Cookie",@"Proxy-Authorization"]){
  ReaderTestTask *private=[ReaderTestTask new];NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:url(@"index.html")];[request setValue:@"test-only" forHTTPHeaderField:header];private.request=request;
  [handler webView:nil startURLSchemeTask:private];check(private.error&&!private.response&&!private.bytes.length,"credential-bearing request refused without delivery");
 }
 ReaderTestTask *post=[ReaderTestTask new];NSMutableURLRequest *postRequest=[NSMutableURLRequest requestWithURL:url(@"index.html")];postRequest.HTTPMethod=@"POST";post.request=postRequest;[handler webView:nil startURLSchemeTask:post];check(post.error&&!post.response,"bundle origin has no mutation method");
 ReaderTestTask *cancelled=[ReaderTestTask new];cancelled.request=[NSURLRequest requestWithURL:url(@"index.html")];[handler webView:nil startURLSchemeTask:cancelled];[handler webView:nil stopURLSchemeTask:cancelled];
 ReaderTestTask *newer=[ReaderTestTask new];newer.request=[NSURLRequest requestWithURL:url(@"index.html")];[handler webView:nil startURLSchemeTask:newer];spin(^BOOL{return newer.finished||newer.error!=nil;});
 check(!cancelled.finished&&!cancelled.error&&!cancelled.response&&!cancelled.bytes.length&&newer.finished,"cancelled older read cannot deliver into newer request");
 ReaderTestTask *reentrant=[ReaderTestTask new];reentrant.request=[NSURLRequest requestWithURL:url(@"index.html")];__weak ReaderTestTask *weak=reentrant;reentrant.onResponse=^{[handler webView:nil stopURLSchemeTask:weak];};
 [handler webView:nil startURLSchemeTask:reentrant];spin(^BOOL{return reentrant.response!=nil;});
 check(reentrant.response&&!reentrant.finished&&!reentrant.bytes.length&&!reentrant.error,"stop during response prevents later data/finish");
 NSMutableArray *pending=[NSMutableArray new];for(int i=0;i<17;i++){ReaderTestTask *request=[ReaderTestTask new];request.request=[NSURLRequest requestWithURL:url(@"index.html")];[pending addObject:request];[handler webView:nil startURLSchemeTask:request];}
 check(((ReaderTestTask *)pending.lastObject).error!=nil,"concurrent local requests are bounded");
 for(ReaderTestTask *request in pending)[handler webView:nil stopURLSchemeTask:request];
 [fm removeItemAtURL:scratch error:NULL];
 printf("Apple reader assets: %d checks, %d failures\n",checks,failures);return failures?1:0;
}}
