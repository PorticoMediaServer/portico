import Foundation
import React
import CryptoKit
import Network
import Security
import Darwin

private func networkDate(_ value: String) -> Date? {
    let parser = ISO8601DateFormatter(); parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let date = parser.date(from: value) { return date }; parser.formatOptions = [.withInternetDateTime]; return parser.date(from: value)
}
private enum NetworkFailure: Error { case invalid, storage, unavailable }
private func decodeURL(_ value: String, count: Int? = nil, limit: Int = 65536) throws -> Data {
    guard !value.isEmpty, value.utf8.count <= limit * 2, value.range(of: "^[A-Za-z0-9_-]+$", options: .regularExpression) != nil else { throw NetworkFailure.invalid }
    let base = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
    guard let bytes = Data(base64Encoded: base + String(repeating: "=", count: (4 - base.count % 4) % 4)), bytes.count <= limit,
          count == nil || bytes.count == count, encodeURL(bytes) == value else { throw NetworkFailure.invalid }
    return bytes
}
private func encodeURL(_ data: Data) -> String { data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") }
private func secret(_ account: String, create: () throws -> Data) throws -> Data {
    let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "tv.getportico.portico.network", kSecAttrAccount as String: account]
    var read = query; read[kSecReturnData as String] = true
    var result: CFTypeRef?; let status = SecItemCopyMatching(read as CFDictionary, &result)
    if status == errSecSuccess, let data = result as? Data { return data }
    guard status == errSecItemNotFound else { throw NetworkFailure.storage }
    let data = try create(); var insert = query
    insert[kSecValueData as String] = data; insert[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    let saved = SecItemAdd(insert as CFDictionary, nil)
    guard saved == errSecSuccess else { throw NetworkFailure.storage }; return data
}

private final class BonjourSearch: NSObject, NetServiceBrowserDelegate, NetServiceDelegate {
    let browser = NetServiceBrowser()
    let type: String
    let done: ([Any]?, String?) -> Void
    var services: [NetService] = []
    var rows: [[String: Any]] = []
    var timer: Timer?
    var completed = false
    init(type: String, done: @escaping ([Any]?, String?) -> Void) { self.type = type; self.done = done; super.init() }
    func start() { browser.delegate = self; browser.searchForServices(ofType: type, inDomain: "local."); timer = Timer.scheduledTimer(withTimeInterval: 3.5, repeats: false) { [weak self] _ in self?.finish(nil) } }
    func finish(_ failure: String?) { guard !completed else { return }; completed = true; timer?.invalidate(); browser.stop(); browser.delegate = nil; for service in services { service.stop(); service.delegate = nil }; done(failure == nil ? rows : nil, failure) }
    func netServiceBrowser(_ browser: NetServiceBrowser, didNotSearch errorDict: [String: NSNumber]) {
        finish(errorDict[NetService.errorCode]?.intValue == -72008 ? "permission_denied" : "discovery_unavailable")
    }
    func netServiceBrowser(_ browser: NetServiceBrowser, didFind service: NetService, moreComing: Bool) {
        guard !completed, services.count < 32 else { return }; services.append(service); service.delegate = self; service.resolve(withTimeout: 2.5)
    }
    func netServiceDidResolveAddress(_ sender: NetService) {
        guard !completed, rows.count < 64, let raw = sender.txtRecordData(), raw.count <= 2048 else { return }
        let txt = NetService.dictionary(fromTXTRecord: raw)
        func field(_ key: String, _ limit: Int = 128) -> String? { guard let data = txt[key], data.count <= limit else { return nil }; return String(data: data, encoding: .utf8) }
        guard field("v") == "1" else { return }
        // A signed-in Portico player on this network. Opaque IDs only: the controller keeps a row
        // only when it matches its own server and its profile's receiver directory, and sending
        // still goes through the server's authorised handoff.
        if type == "_portico-player._tcp." {
            guard let rid = field("rid", 128), rid.range(of: "^[A-Za-z0-9_-]{1,128}$", options: .regularExpression) != nil,
                  let sid = field("sid", 128), sid.range(of: "^[A-Za-z0-9_-]{1,128}$", options: .regularExpression) != nil,
                  let kind = field("kind", 16), ["tv", "phone", "tablet", "computer"].contains(kind) else { return }
            if rows.contains(where: { ($0["receiverId"] as? String) == rid }) { return }
            rows.append(["receiverId": rid, "serverId": sid, "kind": kind, "name": field("name", 96) ?? sender.name]); return
        }
        if type == "_portico-tv._tcp." {
            guard let code = field("code", 9), code.range(of: "^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$", options: .regularExpression) != nil,
                  let request = field("request", 64), let origin = field("issuer", 255), let key = field("key", 43),
                  let expires = field("expires", 32), let date = networkDate(expires), date > Date(), date.timeIntervalSinceNow <= 605 else { return }
            rows.append(["code": code, "requestId": request, "issuer": origin, "publicKey": key, "name": field("name", 96) ?? sender.name, "expiresAt": date.timeIntervalSince1970 * 1000]); return
        }
        guard let id = field("id"), let fp = field("fp", 43), let port = field("port", 5).flatMap(Int.init), let path = field("path", 8), port == sender.port, port > 0, port < 65536 else { return }
        for data in sender.addresses ?? [] {
            guard rows.count < 64 else { break }
            let address: String? = data.withUnsafeBytes { bytes in
                guard let pointer = bytes.baseAddress, bytes.count >= MemoryLayout<sockaddr>.size else { return nil }
                let sa = pointer.assumingMemoryBound(to: sockaddr.self), length = Int(sa.pointee.sa_len)
                guard length > 0, length <= bytes.count else { return nil }
                var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                guard getnameinfo(sa, socklen_t(length), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { return nil }
                return String(cString: host)
            }
            guard let host = address, !host.contains("%") else { continue }
            let literal = host.contains(":") ? "[\(host)]" : host
            rows.append(["serverId": id, "fingerprint": fp, "port": port, "path": path, "baseUrl": "http://\(literal):\(port)", "expiresAt": Date().addingTimeInterval(5).timeIntervalSince1970 * 1000])
        }
    }
}

@objc(PorticoNetwork)
final class PorticoNetwork: RCTEventEmitter, NetServiceDelegate {
    private var searches: [String: BonjourSearch] = [:]
    private var invalidated = false
    private var monitor: NWPathMonitor?
    private var listening = false
    private var lastPath = ""
    private var tvService: NetService?
    private var tvExpiry: Timer?
    private var playerService: NetService?
    override static func requiresMainQueueSetup() -> Bool { true }
    override var methodQueue: DispatchQueue! { DispatchQueue.main }
    override func supportedEvents() -> [String]! { ["portico.network"] }
    override func startObserving() { if !invalidated { listening = true } }
    override func stopObserving() { listening = false }
    override func invalidate() { invalidated = true; listening = false; monitor?.pathUpdateHandler = nil; monitor?.cancel(); monitor = nil; for search in Array(searches.values) { search.finish("cancelled") }; searches.removeAll(); tvExpiry?.invalidate(); tvExpiry = nil; tvService?.stop(); tvService = nil; playerService?.stop(); playerService = nil; super.invalidate() }

    @objc func sha256(_ payload: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        do { resolve(encodeURL(Data(SHA256.hash(data: try decodeURL(payload))))) } catch { reject("crypto_input", "Invalid identity data.", nil) }
    }
    @objc func verify(_ publicKey: String, signature: String, payload: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        do { let key = try Curve25519.Signing.PublicKey(rawRepresentation: decodeURL(publicKey, count: 32)); resolve(key.isValidSignature(try decodeURL(signature, count: 64), for: try decodeURL(payload))) } catch { reject("crypto_input", "Invalid signed identity.", nil) }
    }
    @objc func startMonitoring() {
        guard !invalidated, monitor == nil else { return }; let m = NWPathMonitor(); monitor = m
        m.pathUpdateHandler = { [weak self, weak m] path in DispatchQueue.main.async {
            guard let self, let m, !self.invalidated, self.monitor === m else { return }
            // This stays on the device. Neither raw interface names/addresses nor
            // this keyed network generation are sent to Hosted.
            // The kind of link, as one coarse word, is the only thing about the path that leaves the device:
            // the viewer's own server uses it to apply their per-network quality choice.
            let transport = path.usesInterfaceType(.wiredEthernet) ? "ethernet" : path.usesInterfaceType(.wifi) ? "wifi" : path.usesInterfaceType(.cellular) ? "cellular" : "unknown"
            var parts = [String(describing: path.status), String(path.isExpensive), String(path.isConstrained), transport]
            var first: UnsafeMutablePointer<ifaddrs>?
            if getifaddrs(&first) == 0 { defer { freeifaddrs(first) }; var row = first
                while let current = row { defer { row = current.pointee.ifa_next }; let value = current.pointee
                    guard let addr = value.ifa_addr, addr.pointee.sa_family == UInt8(AF_INET) || addr.pointee.sa_family == UInt8(AF_INET6) else { continue }
                    var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                    if getnameinfo(addr, socklen_t(addr.pointee.sa_len), &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 { parts.append(String(cString: value.ifa_name) + ":" + String(cString: host)) }
                }
            }
            do { let key = try secret("path-hmac-v1") { var data = Data(count: 32); let status = data.withUnsafeMutableBytes { SecRandomCopyBytes(kSecRandomDefault, 32, $0.baseAddress!) }; guard status == errSecSuccess else { throw NetworkFailure.unavailable }; return data }
                let digest = encodeURL(Data(HMAC<SHA256>.authenticationCode(for: Data(parts.sorted().joined(separator: "\n").utf8), using: SymmetricKey(data: key))))
                if digest != self.lastPath { self.lastPath = digest; if self.listening { self.sendEvent(withName: "portico.network", body: ["generation": digest, "online": path.status == .satisfied, "transport": transport]) } }
            } catch { if self.listening { self.sendEvent(withName: "portico.network", body: ["generation": UUID().uuidString, "online": path.status == .satisfied, "transport": transport]) } }
        } }; m.start(queue: DispatchQueue(label: "tv.getportico.network.path"))
    }
    @objc func browse(_ identifier: String, kind: String, resolver resolve: @escaping RCTPromiseResolveBlock, rejecter reject: @escaping RCTPromiseRejectBlock) {
        guard !invalidated, !identifier.isEmpty, identifier.count <= 64, searches[identifier] == nil, searches.count < 2, ["servers", "tv", "players"].contains(kind) else { reject("discovery_busy", "Discovery is already in progress.", nil); return }
        let search = BonjourSearch(type: kind == "tv" ? "_portico-tv._tcp." : kind == "players" ? "_portico-player._tcp." : "_portico._tcp.") { [weak self] rows, failure in
            self?.searches.removeValue(forKey: identifier); if let failure { reject(failure, failure == "permission_denied" ? "Local Network access is denied. Manual connection and TV codes still work." : "Nearby discovery is unavailable.", nil) } else { resolve(rows ?? []) }
        }; searches[identifier] = search; search.start()
    }
    @objc func cancelBrowse(_ identifier: String) { searches[identifier]?.finish("cancelled") }

    // Only the public half leaves Keychain. The operation ID must be durably
    // recorded by the caller before starting an approval request.
    @objc func tvPublicKey(_ requestId: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        do { guard UUID(uuidString: requestId) != nil else { throw NetworkFailure.invalid }; let bytes = try secret("tv-v1:" + requestId) { Curve25519.KeyAgreement.PrivateKey().rawRepresentation }; let key = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: bytes); resolve(encodeURL(key.publicKey.rawRepresentation)) } catch { reject("secure_storage", "TV setup identity could not be saved.", nil) }
    }
    @objc func openTVGrant(_ requestId: String, envelope: NSDictionary, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        do {
            guard UUID(uuidString: requestId) != nil, envelope["kind"] as? String == "portico.tv-grant", envelope["version"] as? Int == 1, envelope["requestId"] as? String == requestId,
                  let issuer = envelope["issuer"] as? String, let serverKey = envelope["publicKey"] as? String, let recipient = envelope["recipientKey"] as? String,
                  let salt = envelope["salt"] as? String, let nonce = envelope["nonce"] as? String, let encrypted = envelope["ciphertext"] as? String else { throw NetworkFailure.invalid }
            // Missing keys are not recreated while opening a response.
            let bytes = try secret("tv-v1:" + requestId) { throw NetworkFailure.storage }
            let local = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: bytes)
            guard encodeURL(local.publicKey.rawRepresentation) == recipient else { throw NetworkFailure.invalid }
            let peer = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: decodeURL(serverKey, count: 32))
            let context = Data(("portico.tv-grant.v1\u{0}" + issuer + "\u{0}" + requestId + "\u{0}" + recipient + "\u{0}" + serverKey).utf8)
            let shared = try local.sharedSecretFromKeyAgreement(with: peer)
            let key = shared.hkdfDerivedSymmetricKey(using: SHA256.self, salt: try decodeURL(salt, count: 32), sharedInfo: context, outputByteCount: 32)
            let cipher = try decodeURL(encrypted, limit: 65536)
            guard cipher.count > 16 else { throw NetworkFailure.invalid }
            let box = try AES.GCM.SealedBox(nonce: AES.GCM.Nonce(data: decodeURL(nonce, count: 12)), ciphertext: cipher.dropLast(16), tag: cipher.suffix(16))
            let plain = try AES.GCM.open(box, using: key, authenticating: context)
            guard let json = String(data: plain, encoding: .utf8) else { throw NetworkFailure.invalid }; resolve(json)
        } catch { reject("tv_grant_invalid", "The TV setup response could not be verified. No sign-in was installed.", nil) }
    }
    @objc func forgetTVKey(_ requestId: String) {
        guard UUID(uuidString: requestId) != nil else { return }; let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "tv.getportico.portico.network", kSecAttrAccount as String: "tv-v1:" + requestId]; SecItemDelete(query as CFDictionary)
    }
    @objc func advertiseTV(_ value: NSDictionary, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        guard let code = value["code"] as? String, code.range(of: "^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{4}$", options: .regularExpression) != nil,
              let request = value["requestId"] as? String, UUID(uuidString: request) != nil, let issuer = value["issuer"] as? String, issuer.utf8.count < 240,
              let key = value["publicKey"] as? String, key.count == 43, let expires = value["expiresAt"] as? String, let expiry = networkDate(expires), expiry > Date(), expiry.timeIntervalSinceNow <= 605 else { reject("tv_discovery_invalid", "Invalid TV discovery hint.", nil); return }
        tvExpiry?.invalidate(); tvService?.stop()
        let name = String((value["name"] as? String ?? "Portico TV").prefix(32))
        let service = NetService(domain: "local.", type: "_portico-tv._tcp.", name: name, port: 9)
        let text = ["v": "1", "code": code, "request": request, "issuer": issuer, "key": key, "expires": expires, "name": name]
        service.setTXTRecord(NetService.data(fromTXTRecord: text.mapValues { Data($0.utf8) })); service.delegate = self; tvService = service; service.publish()
        tvExpiry = Timer.scheduledTimer(withTimeInterval: expiry.timeIntervalSinceNow, repeats: false) { [weak self] _ in self?.stopTVAdvertisement() }; resolve(true)
    }
    /// Play on: this signed-in player is on the network (receiver id, server id, name, kind).
    @objc func advertisePlayer(_ value: NSDictionary, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        let pattern = "^[A-Za-z0-9_-]{1,128}$"
        guard !invalidated, let rid = value["receiverId"] as? String, rid.range(of: pattern, options: .regularExpression) != nil,
              let sid = value["serverId"] as? String, sid.range(of: pattern, options: .regularExpression) != nil,
              let kind = value["kind"] as? String, ["tv", "phone", "tablet", "computer"].contains(kind) else { reject("player_discovery_invalid", "Invalid player advertisement.", nil); return }
        let name = String((value["name"] as? String ?? "Portico").prefix(32))
        if let current = playerService, current.name == name, let old = current.txtRecordData().map(NetService.dictionary(fromTXTRecord:)),
           old["rid"].flatMap({ String(data: $0, encoding: .utf8) }) == rid, old["sid"].flatMap({ String(data: $0, encoding: .utf8) }) == sid { resolve(true); return }
        playerService?.stop()
        let service = NetService(domain: "local.", type: "_portico-player._tcp.", name: name, port: 9)
        let text = ["v": "1", "rid": rid, "sid": sid, "kind": kind, "name": name]
        service.setTXTRecord(NetService.data(fromTXTRecord: text.mapValues { Data($0.utf8) })); service.delegate = self; playerService = service; service.publish(); resolve(true)
    }
    @objc func stopPlayerAdvertisement() { playerService?.stop(); playerService = nil }
    // The owner's own certificate (PorticoServerTrust). JavaScript decides when to pin, after the
    // server proves its identity over the certificate; these only report, prove over and store.
    @objc func refusedCertificate(_ origin: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        resolve(PorticoServerTrust.refused(at: origin).map { ["fingerprint": $0] })
    }
    @objc func pinnedCertificate(_ origin: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        resolve(PorticoServerTrust.pinned(at: origin))
    }
    @objc func pinCertificate(_ origin: String, fingerprint: String, serverId: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        if PorticoServerTrust.pin(origin, fingerprint: fingerprint, serverId: serverId) { resolve(true) } else { reject("certificate_storage", "The server's certificate could not be saved.", nil) }
    }
    @objc func forgetCertificate(_ origin: String) { PorticoServerTrust.forget(origin) }
    @objc func forgetCapturedCertificate(_ origin: String, fingerprint: String, serverId: String, resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        if PorticoServerTrust.forgetCaptured(origin, fingerprint: fingerprint, serverId: serverId) { resolve(true) }
        else { reject("certificate_changed", "certificate_changed", nil) }
    }
    @objc func forgetServerCertificates(_ serverId: String, origins: [String], resolver resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        if PorticoServerTrust.forgetServerCertificates(serverId, origins: origins) { resolve(nil) }
        else { reject("certificate_storage", "certificate_storage", nil) }
    }
    @objc func pinnedCertificates(_ resolve: RCTPromiseResolveBlock, rejecter reject: RCTPromiseRejectBlock) {
        resolve(PorticoServerTrust.allPinned())
    }
    @objc func proofRequest(_ origin: String, fingerprint: String, body: String, resolver resolve: @escaping RCTPromiseResolveBlock, rejecter reject: @escaping RCTPromiseRejectBlock) {
        PorticoServerTrust.prove(origin, fingerprint: fingerprint, body: body) { response in
            if let response = response { resolve(response) } else { reject("certificate_proof", "The server could not prove its identity.", nil) }
        }
    }
    @objc func stopTVAdvertisement() { tvExpiry?.invalidate(); tvExpiry = nil; tvService?.stop(); tvService = nil }
}
