#import <Foundation/Foundation.h>
#import "../Native/PorticoDownloadManifest.h"

// N01: what the download owner restores from its manifest, executed (the owner's `loadManifest`
// assigns exactly this). A manifest that cannot be trusted restores nothing and is never replaced.
// Prints {"assertions":N,"failures":M}.

static int assertions = 0, failures = 0;
static void check(BOOL ok, const char *what) {
  assertions++;
  if (!ok) {failures++; fprintf(stderr, "FAIL %s\n", what);}
}
static NSDictionary *entry(NSString *identifier, id state, id digest, id bytes) {
  NSMutableDictionary *value = [@{@"id": identifier, @"title": @"A title", @"file": @"media.mkv"} mutableCopy];
  if (state) value[@"state"] = state;
  if (digest) value[@"sha256"] = digest;
  if (bytes) value[@"bytes"] = bytes;
  return value;
}
static NSData *json(id object) {return [NSJSONSerialization dataWithJSONObject:object options:0 error:nil];}
static NSString *const digest = @"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

int main(void) {@autoreleasepool {
  // No file: an empty, writable list.
  NSMutableDictionary *none = PorticoDownloadManifestEntries(nil);
  check(none != nil && none.count == 0, "no manifest restores an empty list");

  // Every valid entry comes back whole, in every state, and mutable for the owner.
  NSMutableDictionary *stored = [NSMutableDictionary new];
  NSArray *states = @[@"downloading", @"paused", @"failed", @"ready", @"removed"];
  for (NSUInteger index = 0; index < states.count; index++) {
    NSString *identifier = [NSString stringWithFormat:@"item_%lu-%@", (unsigned long)index, @"x"];
    stored[identifier] = entry(identifier, states[index], digest, @(1 + index * 1000));
  }
  stored[@"large"] = entry(@"large", @"ready", digest, @9007199254740991.0);
  NSMutableDictionary *restored = PorticoDownloadManifestEntries(json(stored));
  check(restored.count == stored.count, "every entry is restored");
  check([restored isEqualToDictionary:stored], "each entry is restored exactly as written");
  NSMutableDictionary *first = restored[@"item_0-x"];
  check([first isKindOfClass:NSMutableDictionary.class], "restored entries are mutable");
  first[@"state"] = @"ready";
  check([restored[@"item_0-x"][@"state"] isEqual:@"ready"], "the owner can update a restored entry in place");

  // Anything untrustworthy restores nothing: the owner refuses work and never writes over it.
  check(PorticoDownloadManifestEntries([NSData data]) == nil, "an empty file is not a manifest");
  check(PorticoDownloadManifestEntries([@"not json" dataUsingEncoding:NSUTF8StringEncoding]) == nil, "text that is not JSON");
  check(PorticoDownloadManifestEntries(json(@[entry(@"a", @"ready", digest, @1)])) == nil, "a list instead of an object");
  const unsigned char invalidUTF8[] = {'{', 0xff, '}'};
  check(PorticoDownloadManifestEntries([NSData dataWithBytes:invalidUTF8 length:sizeof invalidUTF8]) == nil, "bytes that are not UTF-8");
  NSArray<NSDictionary *> *invalid = @[
    @{@"a": entry(@"b", @"ready", digest, @1)},                       // id differs from its key
    @{@"a/b": entry(@"a/b", @"ready", digest, @1)},                   // unsafe identifier
    @{@"": entry(@"", @"ready", digest, @1)},                         // empty identifier
    @{@"a": entry(@"a", @"finished", digest, @1)},                    // unknown state
    @{@"a": entry(@"a", nil, digest, @1)},                            // no state
    @{@"a": entry(@"a", @"ready", @"0123", @1)},                      // short digest
    @{@"a": entry(@"a", @"ready", [digest uppercaseString], @1)},     // upper-case accepted? (see below)
    @{@"a": entry(@"a", @"ready", [digest stringByReplacingOccurrencesOfString:@"f" withString:@"g"], @1)}, // not hex
    @{@"a": entry(@"a", @"ready", digest, @0)},                       // no bytes
    @{@"a": entry(@"a", @"ready", digest, @1.5)},                     // fractional bytes
    @{@"a": entry(@"a", @"ready", digest, @9007199254740992.0)},      // beyond exact integers
    @{@"a": entry(@"a", @"ready", digest, @YES)},                     // a boolean is not a size
    @{@"a": entry(@"a", @"ready", digest, @"12")},                    // a string is not a size
    @{@"a": @"not an entry"},
  ];
  for (NSUInteger index = 0; index < invalid.count; index++) {
    NSMutableDictionary *mixed = [stored mutableCopy];
    [mixed addEntriesFromDictionary:invalid[index]];
    NSMutableDictionary *result = PorticoDownloadManifestEntries(json(mixed));
    // The digest is compared case-insensitively (lowercaseString), so upper-case hex is valid.
    BOOL expectValid = index == 6;
    char what[96]; snprintf(what, sizeof what, "invalid case %lu %s", (unsigned long)index, expectValid ? "is accepted" : "restores nothing");
    check(expectValid ? result.count == mixed.count : result == nil, what);
  }
  printf("{\"assertions\":%d,\"failures\":%d}\n", assertions, failures);
  return failures == 0 ? 0 : 1;
}}
