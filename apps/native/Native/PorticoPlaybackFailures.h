#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#include <errno.h>

// How an AVFoundation failure is reported (ENGINE-CONTRACT.md `error`), independent of a player or a
// clock, so it can be tested as a table (N03), as Android's PorticoPlaybackFailures is. Header-only:
// PorticoPlaybackController.m and apps/native/test/apple-playback-failures-tests.m include it.

NS_ASSUME_NONNULL_BEGIN

static inline NSInteger PorticoHTTPStatusInComment(NSString *comment) {
 if(![comment isKindOfClass:NSString.class]||comment.length>4096)return 0;
 static NSRegularExpression *pattern;static dispatch_once_t once;
 dispatch_once(&once,^{pattern=[NSRegularExpression regularExpressionWithPattern:@"\\bHTTP\\s+(?:error\\s+|status(?:\\s+code)?\\s*[:=]?\\s*)?([45][0-9]{2})\\b" options:NSRegularExpressionCaseInsensitive error:nil];});
 NSTextCheckingResult *match=[pattern firstMatchInString:comment options:0 range:NSMakeRange(0,comment.length)];
 return match?[comment substringWithRange:[match rangeAtIndex:1]].integerValue:0;
}
/** URL failures that waiting will not change. A bad server response is not one of them: with no
 * status to go by it is most often a server that is failing for now. */
static inline BOOL PorticoURLErrorIsPermanent(NSInteger code) {
 return [@[@(NSURLErrorCancelled),@(NSURLErrorFileDoesNotExist),@(NSURLErrorFileIsDirectory),@(NSURLErrorNoPermissionsToReadFile),@(NSURLErrorDataNotAllowed),@(NSURLErrorAppTransportSecurityRequiresSecureConnection),@(NSURLErrorUserAuthenticationRequired),@(NSURLErrorUserCancelledAuthentication),@(NSURLErrorCannotDecodeRawData),@(NSURLErrorCannotDecodeContentData)] containsObject:@(code)];
}
static inline NSInteger PorticoErrorHTTPStatus(NSError *top) {
 NSError *error=top;
 for(int i=0;[error isKindOfClass:NSError.class]&&i<4;i++,error=error.userInfo[NSUnderlyingErrorKey]){
  id response=error.userInfo[@"NSHTTPURLResponse"];
  if([response isKindOfClass:NSHTTPURLResponse.class]){NSInteger status=[response statusCode];if(status>=400&&status<=599)return status;}
  NSInteger status=PorticoHTTPStatusInComment(error.userInfo[NSLocalizedDescriptionKey]);
  if(!status)status=PorticoHTTPStatusInComment(error.userInfo[NSLocalizedFailureReasonErrorKey]);
  if(status)return status;
  // HLS can wrap these HTTP failures as CoreMedia errors rather than URL errors.
  if([error.domain isEqual:@"CoreMediaErrorDomain"]){if(error.code==-12938)return 404;if(error.code==-12660)return 403;}
 }
 return 0;
}

/** One failure, classified. A network failure is weather and is retried on the same route. An
 * engine failure (the decoder or the parser refusing what it was given) carries its class and the
 * underlying code, so the app can tell the server which route failed and ask for the next one
 * instead of showing the viewer an error. `logRecent` says the item's last error-log event is
 * under 15 seconds old; its domain, status code and comment follow. */
static inline NSDictionary<NSString *, id> *PorticoPlaybackFailureFacts(NSError *top, BOOL logRecent, NSString *_Nullable logDomain, NSInteger logStatusCode, NSString *_Nullable logComment) {
 NSString *engine=@"";
 if([top.domain isEqual:AVFoundationErrorDomain]){
  if(top.code==AVErrorDecodeFailed||top.code==AVErrorDecoderNotFound||top.code==AVErrorDecoderTemporarilyUnavailable)engine=@"decode_error";
  else if(top.code==AVErrorFileFormatNotRecognized||top.code==AVErrorFailedToParse||top.code==AVErrorFormatUnsupported)engine=@"source_not_supported";
 }
 NSInteger httpStatus=PorticoErrorHTTPStatus(top);
 // Only a recent log entry can be about this failure, and never when the engine has named a decoder
 // or parser fault itself: an old 404 or 503 must not hide that, or send playback looking for
 // another route.
 BOOL fresh=logRecent&&!engine.length;
 if(!httpStatus&&fresh)httpStatus=PorticoHTTPStatusInComment(logComment);
 if(!httpStatus&&fresh&&logStatusCode>=400&&logStatusCode<=599)httpStatus=logStatusCode;
 // The outermost error that says anything decides. A deeper code never turns a lost connection
 // into a media failure, and a socket error is the connection unless it is a missing or forbidden
 // file: a connection dropped while the app was in the background is weather, not the media.
 BOOL network=NO,delivery=NO;NSError *error=top;
 for(int i=0;[error isKindOfClass:NSError.class]&&i<4&&!network&&!delivery;i++,error=error.userInfo[NSUnderlyingErrorKey]){
  if([error.domain isEqual:NSURLErrorDomain]){
   if(PorticoURLErrorIsPermanent(error.code))delivery=YES;else network=YES;
  }
  if([error.domain isEqual:NSPOSIXErrorDomain]){
   if(error.code==ENOENT||error.code==EACCES||error.code==EPERM||error.code==EISDIR)delivery=YES;else network=YES;
  }
  if([error.domain isEqual:NSCocoaErrorDomain]&&(error.code==NSFileReadNoPermissionError||error.code==NSFileReadNoSuchFileError))delivery=YES;
  if([error.domain isEqual:AVFoundationErrorDomain]&&error.code==AVErrorContentIsUnavailable)delivery=YES;
 }
 // A stream's transport failure can arrive as a media error whose cause is only in the log.
 if(!network&&!delivery&&!httpStatus&&fresh&&[logDomain isEqual:NSURLErrorDomain]){
  if(PorticoURLErrorIsPermanent(logStatusCode))delivery=YES;else network=YES;
 }
 if(httpStatus){delivery=YES;network=httpStatus>=500||httpStatus==408||httpStatus==429;}
 // Nothing says why it failed, but something says it was not the media: the system interrupted
 // playback (session interrupted -11818, media services reset -11819, operation interrupted
 // -11847, out of memory -11801), or the transport was reporting trouble moments ago. The app
 // must not take that for an incompatibility and tell the server so.
 BOOL interrupted=[top.domain isEqual:AVFoundationErrorDomain]&&(top.code==-11818||top.code==-11819||top.code==-11847||top.code==-11801);
 BOOL uncertain=!network&&!delivery&&!httpStatus&&!engine.length&&(interrupted||fresh);
 NSError *under=top.userInfo[NSUnderlyingErrorKey];
 NSString *detail=[NSString stringWithFormat:@"%@ %ld%@",top.domain?:@"",(long)top.code,under?[NSString stringWithFormat:@" / %@ %ld",under.domain,(long)under.code]:@""];
 return @{@"networkError":@(network),@"deliveryError":@(delivery),@"httpStatus":@(httpStatus),@"uncertain":@(uncertain),@"engineCode":(network||delivery)?@"":engine,@"errorDetail":detail,@"error":network?@"playback_connection_interrupted":@"playback_failed"};
}

NS_ASSUME_NONNULL_END
