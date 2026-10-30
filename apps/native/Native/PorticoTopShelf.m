#import <React/RCTBridgeModule.h>
#import <Security/Security.h>
#import <TargetConditionals.h>
#if TARGET_OS_TV
#import <TVServices/TVServices.h>
#endif

/** Hands the home-screen shelf what it needs and nothing more: the server's address (shared
 * defaults, not secret) and a token that reads one small feed. SEC-19: the token lives in the
 * keychain access group the app shares with its shelf extension (the app group identifier is
 * also a keychain access group), not in shared defaults. */
@interface PorticoTopShelf : NSObject <RCTBridgeModule> @end
@implementation PorticoTopShelf
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }
static NSString *const PorticoTopShelfGroup = @"group.tv.getportico.portico";

static NSDictionary *PorticoTopShelfQuery(void) {
  return @{(__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword, (__bridge id)kSecAttrService: @"tv.getportico.topshelf", (__bridge id)kSecAttrAccount: @"feed", (__bridge id)kSecAttrAccessGroup: PorticoTopShelfGroup};
}
static BOOL PorticoTopShelfStoreToken(NSString *token) {
  SecItemDelete((__bridge CFDictionaryRef)PorticoTopShelfQuery());
  if (!token.length) return YES;
  NSMutableDictionary *item = [PorticoTopShelfQuery() mutableCopy];
  item[(__bridge id)kSecValueData] = [token dataUsingEncoding:NSUTF8StringEncoding];
  item[(__bridge id)kSecAttrAccessible] = (__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly;
  return SecItemAdd((__bridge CFDictionaryRef)item, NULL) == errSecSuccess;
}
static BOOL PorticoTopShelfHasToken(void) {
  NSMutableDictionary *query = [PorticoTopShelfQuery() mutableCopy];
  query[(__bridge id)kSecMatchLimit] = (__bridge id)kSecMatchLimitOne;
  return SecItemCopyMatching((__bridge CFDictionaryRef)query, NULL) == errSecSuccess;
}

#if TARGET_OS_TV
/** The shelf's cached feed and posters: another viewer's must never be drawn. */
static void PorticoTopShelfClearCache(void) {
  NSURL *caches = [[NSFileManager.defaultManager containerURLForSecurityApplicationGroupIdentifier:PorticoTopShelfGroup] URLByAppendingPathComponent:@"Library/Caches" isDirectory:YES];
  if (!caches) return;
  [NSFileManager.defaultManager removeItemAtURL:[caches URLByAppendingPathComponent:@"topshelf" isDirectory:YES] error:nil];
}
#endif

/** `owner` is whose shelf this is (server and profile): a token issued to another viewer, and
 * what was drawn for them, is replaced rather than reused. */
RCT_REMAP_METHOD(configure, configure:(NSString *)baseUrl token:(NSString *)token expiresAt:(NSString *)expiresAt owner:(NSString *)owner resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
#if TARGET_OS_TV
 if (![baseUrl isKindOfClass:NSString.class] || ![token isKindOfClass:NSString.class] || ![expiresAt isKindOfClass:NSString.class] || ![owner isKindOfClass:NSString.class] || token.length > 8192 || owner.length > 512) { reject(@"topshelf_invalid", @"The shelf configuration is invalid.", nil); return; }
 NSUserDefaults *defaults = [[NSUserDefaults alloc] initWithSuiteName:PorticoTopShelfGroup];
 // If the shared keychain group is unavailable (an unsigned build), keep the shelf working from
 // shared defaults as before, and say so in the log rather than silently leaving it empty.
 if (PorticoTopShelfStoreToken(token)) [defaults removeObjectForKey:@"topshelf.token"];
 else { NSLog(@"[TopShelf] shared keychain unavailable; storing the feed token in shared defaults"); [defaults setObject:token forKey:@"topshelf.token"]; }
 if (![[defaults stringForKey:@"topshelf.owner"] isEqualToString:owner]) PorticoTopShelfClearCache();
 [defaults setObject:baseUrl forKey:@"topshelf.baseUrl"]; [defaults setObject:expiresAt forKey:@"topshelf.expiresAt"]; [defaults setObject:owner forKey:@"topshelf.owner"];
 [TVTopShelfContentProvider topShelfContentDidChange];
#endif
 resolve(nil);
}
/** The extension follows the resolved app catalogue, independent of the system locale. */
RCT_EXPORT_METHOD(setLocale:(NSString *)tag) {
#if TARGET_OS_TV
 if(![tag isKindOfClass:NSString.class]||!tag.length||tag.length>64||[tag rangeOfString:@"^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$" options:NSRegularExpressionSearch].location==NSNotFound)return;
 NSUserDefaults *defaults=[[NSUserDefaults alloc]initWithSuiteName:PorticoTopShelfGroup];
 if([[defaults stringForKey:@"topshelf.locale"] isEqualToString:tag])return;
 [defaults setObject:tag forKey:@"topshelf.locale"];[TVTopShelfContentProvider topShelfContentDidChange];
#endif
}
RCT_REMAP_METHOD(expiresAt, expiresAtWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
 NSUserDefaults *defaults = [[NSUserDefaults alloc] initWithSuiteName:PorticoTopShelfGroup];
#if TARGET_OS_TV
 // Move a token written by an earlier build out of shared defaults.
 NSString *legacy = [defaults stringForKey:@"topshelf.token"];
 if (legacy.length && PorticoTopShelfStoreToken(legacy)) { [defaults removeObjectForKey:@"topshelf.token"]; [TVTopShelfContentProvider topShelfContentDidChange]; }
 // Without a token the shelf can't draw anything, so report nothing held and let the app renew.
 if (!PorticoTopShelfHasToken() && ![defaults stringForKey:@"topshelf.token"].length) { resolve((id)[NSNull null]); return; }
#endif
 NSString *base = [defaults stringForKey:@"topshelf.baseUrl"], *expires = [defaults stringForKey:@"topshelf.expiresAt"], *owner = [defaults stringForKey:@"topshelf.owner"] ?: @"";
 resolve(base && expires ? @{@"baseUrl": base, @"expiresAt": expires, @"owner": owner} : (id)[NSNull null]);
}
RCT_REMAP_METHOD(clear, clearWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
#if TARGET_OS_TV
 NSUserDefaults *defaults = [[NSUserDefaults alloc] initWithSuiteName:PorticoTopShelfGroup];
 for (NSString *key in @[@"topshelf.baseUrl", @"topshelf.token", @"topshelf.expiresAt", @"topshelf.owner"]) [defaults removeObjectForKey:key];
 PorticoTopShelfStoreToken(nil);
 PorticoTopShelfClearCache();
 [TVTopShelfContentProvider topShelfContentDidChange];
#endif
 resolve(nil);
}
/** Called after something was watched, so the shelf's Continue Watching is not a day old. */
RCT_EXPORT_METHOD(changed) {
#if TARGET_OS_TV
 [TVTopShelfContentProvider topShelfContentDidChange];
#endif
}
@end
