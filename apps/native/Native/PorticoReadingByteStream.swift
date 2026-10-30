import Foundation

private actor ReadingByteBody {
    private var iterator: URLSession.AsyncBytes.AsyncIterator?
    private var eof = false
    private var retired = false

    init(_ bytes: URLSession.AsyncBytes) { iterator = bytes.makeAsyncIterator() }

    func read() async throws -> (Data, Bool) {
        if eof { return (Data(), true) }
        guard !retired, var current = iterator else { throw CancellationError() }
        var data = Data(capacity: 49_152)
        while data.count < 49_152 {
            guard let byte = try await current.next() else { eof = true; break }
            data.append(byte)
        }
        guard !retired else { throw CancellationError() }
        iterator = current
        return (data, eof)
    }

    func retire() { retired = true; iterator = nil }
}

/** The task delegate forwards the existing certificate policy and never redirects.
 * Callback state belongs to the queue request and is protected by that request's lock. */
private final class ReadingBytePolicy: NSObject, URLSessionTaskDelegate {
    let attached: (URLSessionTask) -> Void
    let failure: (Error) -> Void
    init(attached: @escaping (URLSessionTask) -> Void, failure: @escaping (Error) -> Void) {
        self.attached = attached; self.failure = failure
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        attached(task)
        PorticoServerTrust.handle(challenge, completion: completionHandler)
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        attached(task); completionHandler(nil)
        failure(NSError(domain: "PorticoReadingStream", code: 1, userInfo: [NSLocalizedDescriptionKey: "network_stream_redirect"]))
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        // Completion must not reattach/re-cancel a request already retired by its owner.
        if let error { failure(error) }
    }
}

/** No independent client: this driver receives the queue's session and captured request.
 * Own data is at most one 48KiB pull. The same cancellation covers metadata resolution,
 * including Foundation's wait for the first available body byte or end of body. */
@objc(PorticoReadingByteStream)
final class PorticoReadingByteStream: NSObject, PorticoReadingStreamDriver {
    private let lock = NSLock()
    private var closed = false
    private var opening: Task<Void, Never>?
    private var reading: Task<Void, Never>?
    private var body: ReadingByteBody?
    private var task: URLSessionTask?
    private var policy: ReadingBytePolicy?
    private var readPending = false

    private func locked<T>(_ operation: () -> T) -> T {
        lock.lock(); defer { lock.unlock() }; return operation()
    }

    func open(session: URLSession, request: URLRequest, attached: @escaping (URLSessionTask) -> Void,
              headers: @escaping (HTTPURLResponse) -> Void, failure: @escaping (Error) -> Void) {
        locked {
            guard !closed, opening == nil else { return }
            let policy = ReadingBytePolicy(attached: attached, failure: failure)
            self.policy = policy
            opening = Task { [weak self] in
                guard let self else { return }
                do {
                    let (bytes, response) = try await session.bytes(for: request, delegate: policy)
                    guard let http = response as? HTTPURLResponse else {
                        bytes.task.cancel()
                        throw NSError(domain: "PorticoReadingStream", code: 2, userInfo: [NSLocalizedDescriptionKey: "network_stream_response_invalid"])
                    }
                    let current = self.locked {
                        guard !self.closed else { return false }
                        self.task = bytes.task; self.body = ReadingByteBody(bytes); self.opening = nil; return true
                    }
                    guard current else { bytes.task.cancel(); return }
                    attached(bytes.task); headers(http)
                } catch {
                    if self.locked({ self.opening = nil; return !self.closed }) { failure(error) }
                }
            }
        }
    }

    func read(completion: @escaping (Data?, Bool, Error?) -> Void) {
        let source = locked { () -> (ReadingByteBody?, Task<Void, Never>?)? in
            guard !closed, !readPending, body != nil || opening != nil else { return nil }
            readPending = true; return (body, opening)
        }
        guard let source else {
            completion(nil, false, NSError(domain: "PorticoReadingStream", code: 3, userInfo: [NSLocalizedDescriptionKey: "network_stream_retired"]))
            return
        }
        let pending = Task { [weak self] in
            guard let self else { return }
            do {
                if source.0 == nil { await source.1?.value }
                guard let active = source.0 ?? self.locked({ self.closed ? nil : self.body }) else {
                    if !self.locked({ self.closed }) {
                        throw NSError(domain: "PorticoReadingStream", code: 3, userInfo: [NSLocalizedDescriptionKey: "network_stream_retired"])
                    }
                    return
                }
                let (data, eof) = try await active.read()
                let current = self.locked { self.readPending = false; self.reading = nil; return !self.closed }
                if current { completion(data, eof, nil) }
            } catch {
                let current = self.locked { self.readPending = false; self.reading = nil; return !self.closed }
                if current { completion(nil, false, error) }
            }
        }
        locked { if closed { pending.cancel() } else if readPending { reading = pending } }
    }

    func cancel() {
        let pending = locked { () -> (Task<Void, Never>?, Task<Void, Never>?, URLSessionTask?, ReadingByteBody?) in
            closed = true; let pending = (opening, reading, task, body)
            opening = nil; reading = nil; task = nil; body = nil; return pending
        }
        pending.0?.cancel(); pending.1?.cancel(); pending.2?.cancel()
        if let body = pending.3 { Task { await body.retire() } }
    }
}
