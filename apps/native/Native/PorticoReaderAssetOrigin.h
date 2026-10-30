#import <Foundation/Foundation.h>
#import <WebKit/WebKit.h>

NS_ASSUME_NONNULL_BEGIN
FOUNDATION_EXPORT NSString *const PorticoReaderOrigin;
FOUNDATION_EXPORT NSData * _Nullable PorticoReaderReadAsset(NSURL *root, NSURL *url, NSString * _Nullable * _Nullable mime, NSError * _Nullable * _Nullable error);
FOUNDATION_EXPORT NSURL * _Nullable PorticoReaderEntryURL(void);
FOUNDATION_EXPORT void PorticoConfigureReaderWebView(WKWebViewConfiguration *configuration);

// root is supplied by the app bundle, never by JavaScript or a media server.
@interface PorticoReaderAssetOrigin : NSObject <WKURLSchemeHandler>
- (instancetype)initWithRoot:(NSURL *)root;
@end
NS_ASSUME_NONNULL_END
