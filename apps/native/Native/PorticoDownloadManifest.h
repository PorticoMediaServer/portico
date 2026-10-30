#import <Foundation/Foundation.h>
#include <math.h>

// What the download manifest restores, independent of the owner, its session and its queue, so the
// restoration can be run as a test (N01, apps/native/test/apple-download-manifest-tests.m).
// Header-only: PorticoDownloads.m includes it.

NS_ASSUME_NONNULL_BEGIN

static inline BOOL PorticoSafeIdentifier(NSString *value) {
  if (![value isKindOfClass:NSString.class] || value.length < 1 || value.length > 160) return NO;
  NSCharacterSet *allowed = [NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"];
  return [value rangeOfCharacterFromSet:allowed.invertedSet].location == NSNotFound;
}

static inline BOOL PorticoDownloadValidEntry(NSString *identifier, NSDictionary *entry) {
  if (!PorticoSafeIdentifier(identifier) || ![entry isKindOfClass:NSDictionary.class] || ![entry[@"id"] isEqual:identifier]) return NO;
  if (![@[@"downloading", @"paused", @"failed", @"ready", @"removed"] containsObject:entry[@"state"] ?: NSNull.null]) return NO;
  NSString *digest = entry[@"sha256"];
  if (![digest isKindOfClass:NSString.class] || digest.length != 64 || [digest.lowercaseString rangeOfCharacterFromSet:[NSCharacterSet characterSetWithCharactersInString:@"0123456789abcdef"].invertedSet].location != NSNotFound) return NO;
  NSNumber *bytes = entry[@"bytes"];
  return [bytes isKindOfClass:NSNumber.class] && CFGetTypeID((__bridge CFTypeRef)bytes) != CFBooleanGetTypeID()
    && isfinite(bytes.doubleValue) && bytes.doubleValue >= 1 && bytes.doubleValue <= 9007199254740991.0 && floor(bytes.doubleValue) == bytes.doubleValue;
}

/** The entries a manifest restores: none when there is no file (`raw` nil), nil when the file
 * exists and cannot be trusted (not UTF-8 JSON, not an object, or any entry invalid). Nothing may
 * then be decided from the manifest or written over it. */
static inline NSMutableDictionary<NSString *, NSMutableDictionary *> *_Nullable PorticoDownloadManifestEntries(NSData *_Nullable raw) {
  NSMutableDictionary<NSString *, NSMutableDictionary *> *entries = [NSMutableDictionary new];
  if (!raw) return entries;
  NSString *text = [[NSString alloc] initWithData:raw encoding:NSUTF8StringEncoding];
  NSDictionary *stored = text ? [NSJSONSerialization JSONObjectWithData:raw options:0 error:nil] : nil;
  if (![stored isKindOfClass:NSDictionary.class]) return nil;
  for (NSString *key in stored) {
    if (!PorticoDownloadValidEntry(key, stored[key])) return nil;
    entries[key] = [stored[key] mutableCopy];
  }
  return entries;
}

NS_ASSUME_NONNULL_END
