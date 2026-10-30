#import "PorticoReaderAssetOrigin.h"
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#include <errno.h>
#import <CommonCrypto/CommonDigest.h>

NSString *const PorticoReaderOrigin=@"portico-reader://bundle/index.html";
static const off_t ReaderAssetLimit=64LL*1024*1024;
static NSError *ReaderError(NSString *code){return [NSError errorWithDomain:@"PorticoReaderAssets" code:1 userInfo:@{NSLocalizedDescriptionKey:code}];}

static NSArray<NSString *> *ReaderPath(NSURL *url){
 if(!url||url.absoluteString.length>4096)return nil;
 NSURLComponents *c=[NSURLComponents componentsWithURL:url resolvingAgainstBaseURL:NO];
 if(![c.scheme isEqual:@"portico-reader"]||![c.host isEqual:@"bundle"]||c.user||c.password||c.port||c.query||c.fragment)return nil;
 NSString *path=c.percentEncodedPath;
 if(![path hasPrefix:@"/"]||path.length>2048||[path containsString:@"%"]||[path containsString:@"\\"])return nil;
 NSArray *parts=[[path substringFromIndex:1] componentsSeparatedByString:@"/"];
 if(parts.count>32)return nil;
 NSCharacterSet *invalid=[[NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-"] invertedSet];
 for(NSString *part in parts)if(!part.length||[part hasPrefix:@"."]||[part rangeOfCharacterFromSet:invalid].location!=NSNotFound)return nil;
 return parts;
}
static NSString *ReaderMIME(NSString *name){
 if(!name)return nil;
 static NSDictionary *types;static dispatch_once_t once;
 dispatch_once(&once,^{types=@{@"html":@"text/html",@"js":@"text/javascript",@"mjs":@"text/javascript",@"css":@"text/css",@"json":@"application/json",@"map":@"application/json",@"wasm":@"application/wasm",@"png":@"image/png",@"jpg":@"image/jpeg",@"jpeg":@"image/jpeg",@"gif":@"image/gif",@"webp":@"image/webp",@"svg":@"image/svg+xml",@"woff":@"font/woff",@"woff2":@"font/woff2",@"ttf":@"font/ttf",@"otf":@"font/otf",@"txt":@"text/plain",@"pdf":@"application/pdf",@"epub":@"application/epub+zip",@"cbz":@"application/vnd.comicbook+zip"};});
 return types[name.pathExtension.lowercaseString];
}
static NSString *ReaderCSP(NSData *data,NSString *mime){
 NSMutableString *scripts=[NSMutableString stringWithString:@"script-src 'self' blob:"];
 if([mime isEqual:@"text/html"]){
  // Hash only immutable bundled HTML's authored inline bootstrap. Do not
  // enable arbitrary inline scripts in rendered book content.
  NSString *html=[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
  html=[[html stringByReplacingOccurrencesOfString:@"\r\n" withString:@"\n"] stringByReplacingOccurrencesOfString:@"\r" withString:@"\n"];
  NSRegularExpression *tags=[NSRegularExpression regularExpressionWithPattern:@"<script\\b[^>]*>([\\s\\S]*?)</script\\s*>" options:NSRegularExpressionCaseInsensitive error:NULL];
  for(NSTextCheckingResult *tag in [tags matchesInString:html?:@"" options:0 range:NSMakeRange(0,html.length)]){
   NSData *body=[[html substringWithRange:[tag rangeAtIndex:1]] dataUsingEncoding:NSUTF8StringEncoding];unsigned char digest[CC_SHA256_DIGEST_LENGTH];CC_SHA256(body.bytes,(CC_LONG)body.length,digest);
   [scripts appendFormat:@" 'sha256-%@'",[[NSData dataWithBytes:digest length:sizeof(digest)] base64EncodedStringWithOptions:0]];
  }
 }
 return [NSString stringWithFormat:@"default-src 'none'; %@; worker-src 'self' blob:; connect-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data: blob:; frame-src blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",scripts];
}
static NSData *ReaderRead(NSURL *root,NSURL *url,NSString **mime,NSError **error,BOOL(^cancelled)(void)){
 NSArray *parts=ReaderPath(url);NSString *type=ReaderMIME(parts.lastObject);
 if(!root.isFileURL||!parts||!type){if(error)*error=ReaderError(@"reader_asset_request_invalid");return nil;}
 int fd=open(root.fileSystemRepresentation,O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC);
 if(fd<0){if(error)*error=ReaderError(@"reader_assets_unavailable");return nil;}
 for(NSUInteger i=0;i<parts.count;i++){
  BOOL last=i+1==parts.count;
  int next=openat(fd,[parts[i] fileSystemRepresentation],O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK|(last?0:O_DIRECTORY));close(fd);fd=next;
  if(fd<0)break;
 }
 struct stat info;
 if(fd<0||fstat(fd,&info)!=0||!S_ISREG(info.st_mode)||info.st_size<0||info.st_size>ReaderAssetLimit){if(fd>=0)close(fd);if(error)*error=ReaderError(@"reader_asset_unavailable");return nil;}
 NSMutableData *data=[NSMutableData dataWithLength:(NSUInteger)info.st_size];NSUInteger at=0;
 while(at<data.length){
  if(cancelled&&cancelled()){close(fd);if(error)*error=ReaderError(@"reader_asset_cancelled");return nil;}
  ssize_t size=read(fd,(uint8_t *)data.mutableBytes+at,MIN((NSUInteger)1024*1024,data.length-at));
  if(size<0&&errno==EINTR)continue;
  if(size<=0){close(fd);if(error)*error=ReaderError(@"reader_asset_read_failed");return nil;}
  at+=(NSUInteger)size;
 }
 close(fd);if(mime)*mime=type;return data;
}
NSData *PorticoReaderReadAsset(NSURL *root,NSURL *url,NSString **mime,NSError **error){return ReaderRead(root,url,mime,error,nil);}

static NSURL *ReaderBundleRoot(void){return [NSBundle.mainBundle.resourceURL URLByAppendingPathComponent:@"PorticoReader.bundle" isDirectory:YES];}
NSURL *PorticoReaderEntryURL(void){NSURL *entry=[NSURL URLWithString:PorticoReaderOrigin];return PorticoReaderReadAsset(ReaderBundleRoot(),entry,NULL,NULL).length?entry:nil;}
void PorticoConfigureReaderWebView(WKWebViewConfiguration *configuration){
 // This registers only a custom local scheme. HTTP(S), trust, cookies and the
 // rest of the upstream configuration are untouched.
 [configuration setURLSchemeHandler:[[PorticoReaderAssetOrigin alloc] initWithRoot:ReaderBundleRoot()] forURLScheme:@"portico-reader"];
}

@interface ReaderSchemeRequest : NSObject
@property(nonatomic,strong) id<WKURLSchemeTask> task;
@property(nonatomic) BOOL cancelled;
@end
@implementation ReaderSchemeRequest @end

@implementation PorticoReaderAssetOrigin {
 NSURL *_root;dispatch_queue_t _reads;NSMutableDictionary<NSValue *,ReaderSchemeRequest *> *_requests;
}
- (instancetype)initWithRoot:(NSURL *)root{
 if((self=[super init])){_root=root;_reads=dispatch_queue_create("tv.getportico.reader-assets",DISPATCH_QUEUE_SERIAL);_requests=[NSMutableDictionary new];}return self;
}
- (void)finish:(ReaderSchemeRequest *)state{
 id<WKURLSchemeTask> task=state.task;if(task)[_requests removeObjectForKey:[NSValue valueWithNonretainedObject:task]];
 @synchronized(state){state.cancelled=YES;state.task=nil;}
}
- (void)webView:(WKWebView *)webView startURLSchemeTask:(id<WKURLSchemeTask>)task{
 if(_requests.count>=16){[task didFailWithError:ReaderError(@"reader_asset_requests_exhausted")];return;}
 NSURLRequest *request=task.request;
 if(![@[@"GET",@"HEAD"] containsObject:request.HTTPMethod]||request.HTTPBody.length||request.HTTPBodyStream||[request valueForHTTPHeaderField:@"Authorization"]||[request valueForHTTPHeaderField:@"Cookie"]||[request valueForHTTPHeaderField:@"Proxy-Authorization"]){[task didFailWithError:ReaderError(@"reader_asset_request_invalid")];return;}
 ReaderSchemeRequest *state=[ReaderSchemeRequest new];state.task=task;
 _requests[[NSValue valueWithNonretainedObject:task]]=state;
 dispatch_async(_reads,^{
  __block NSError *error=nil;NSString *mime=nil;
  NSData *data=ReaderRead(self->_root,request.URL,&mime,&error,^BOOL{@synchronized(state){return state.cancelled;}});
  dispatch_async(dispatch_get_main_queue(),^{
   if(state.cancelled)return;
   if(!data){[state.task didFailWithError:error];[self finish:state];return;}
   NSDictionary *headers=@{@"Content-Type":mime,@"Content-Length":[@(data.length) stringValue],@"Cache-Control":@"no-store",@"X-Content-Type-Options":@"nosniff",@"Referrer-Policy":@"no-referrer",
    // Bundled assets contain no session or private book data. Custom-scheme
    // WebKit origins may serialize as null; never enable credentials here.
    @"Access-Control-Allow-Origin":@"*",
    @"Content-Security-Policy":ReaderCSP(data,mime)};
   NSHTTPURLResponse *response=[[NSHTTPURLResponse alloc] initWithURL:request.URL statusCode:200 HTTPVersion:@"HTTP/1.1" headerFields:headers];
   [state.task didReceiveResponse:response];
   if(state.cancelled)return;
   if(![request.HTTPMethod isEqual:@"HEAD"])[state.task didReceiveData:data];
   if(state.cancelled)return;
   [state.task didFinish];[self finish:state];
  });
 });
}
- (void)webView:(WKWebView *)webView stopURLSchemeTask:(id<WKURLSchemeTask>)task{
 ReaderSchemeRequest *state=_requests[[NSValue valueWithNonretainedObject:task]];if(state)[self finish:state];
}
@end
