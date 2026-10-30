#import <Foundation/Foundation.h>
#import "PorticoServerTrust.h"
NS_ASSUME_NONNULL_BEGIN

/** Private body driver for the existing queue request/session, with no URL or authority
 * argument on reads. Open may publish the actual metadata at the first available body byte
 * or end of body. Bytes stay native until an explicit read; each result is at most 48KiB.
 * The original request ID, owner, trust and 15s deadline cover opening and subsequent reads.
 * Swift's byte sequence preserves HTTP decoding and from-start bytes; it is not a file cache. */
@protocol PorticoReadingStreamDriver <NSObject>
- (void)openWithSession:(NSURLSession *)session request:(NSURLRequest *)request
              attached:(void (^)(NSURLSessionTask *task))attached
               headers:(void (^)(NSHTTPURLResponse *response))headers
               failure:(void (^)(NSError *error))failure NS_SWIFT_NAME(open(session:request:attached:headers:failure:));
- (void)readWithCompletion:(void (^)(NSData * _Nullable data,BOOL eof,NSError * _Nullable error))completion NS_SWIFT_NAME(read(completion:));
- (void)cancel;
@end
NS_ASSUME_NONNULL_END
