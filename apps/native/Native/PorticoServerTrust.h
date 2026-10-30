#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>

NS_ASSUME_NONNULL_BEGIN

/**
 * The owner's own certificate. A Portico server may present a certificate no public authority
 * signed (self-signed, or from the owner's private authority). The app trusts such a certificate
 * for exactly the address it was seen at, once a verified Portico identity proof binds its
 * fingerprint and satisfies the native identity policy, and keeps it pinned there.
 *
 * Every URLSession delegate in the app answers server-trust challenges through
 * +handleChallenge:completion:, and AVFoundation through +resourceLoaderDelegate:
 *  - a certificate the system trusts for the name gets the default handling, as before;
 *  - otherwise the leaf certificate's SHA-256 must equal the one pinned for that host and port;
 *  - system refusal is remembered (only in memory), including when a Portico pin permits TLS,
 *    so the caller never mistakes a Portico pin for system trust.
 *
 * Pins live in the app group's defaults, so the Top Shelf extension reads the same ones.
 */
@interface PorticoServerTrust : NSObject
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition disposition, NSURLCredential *_Nullable credential))completion NS_SWIFT_NAME(handle(_:completion:));
/** An ephemeral, cookie-less session whose delegate only answers trust challenges. */
+ (NSURLSession *)session;
/** The delegate (and its queue) to give an AVURLAsset's resource loader. */
+ (id<AVAssetResourceLoaderDelegate>)resourceLoaderDelegate;
+ (dispatch_queue_t)resourceLoaderQueue;
/** Whether a failed task failed because the server's certificate was refused. */
+ (BOOL)isRefusal:(nullable NSError *)error;

+ (nullable NSString *)keyForOrigin:(nullable NSString *)origin;
+ (nullable NSString *)refusedAt:(nullable NSString *)origin NS_SWIFT_NAME(refused(at:));
+ (nullable NSDictionary<NSString *, NSString *> *)pinnedAt:(nullable NSString *)origin NS_SWIFT_NAME(pinned(at:));
+ (BOOL)pin:(nullable NSString *)origin fingerprint:(nullable NSString *)fingerprint serverId:(nullable NSString *)serverId NS_SWIFT_NAME(pin(_:fingerprint:serverId:));
+ (void)forget:(nullable NSString *)origin NS_SWIFT_NAME(forget(_:));
+ (BOOL)forgetCaptured:(NSString *)origin fingerprint:(NSString *)fingerprint serverId:(NSString *)serverId NS_SWIFT_NAME(forgetCaptured(_:fingerprint:serverId:));
/** Offline, one-record removal by server ID and named origins; YES acknowledges persistence. */
+ (BOOL)forgetServerCertificates:(NSString *)serverId origins:(NSArray<NSString *> *)origins NS_SWIFT_NAME(forgetServerCertificates(_:origins:));
/** Every pinned certificate: `address` (host:port), `fingerprint` and `serverId`, in address order. */
+ (NSArray<NSDictionary<NSString *, NSString *> *> *)allPinned NS_SWIFT_NAME(allPinned());
/** The identity proof, and only it, over one named certificate (see PorticoNetwork.proofRequest). */
+ (void)prove:(NSString *)origin fingerprint:(NSString *)fingerprint body:(NSString *)body completion:(void (^)(NSDictionary *_Nullable response))completion NS_SWIFT_NAME(prove(_:fingerprint:body:completion:));
@end

NS_ASSUME_NONNULL_END
