// VDEClient.swift: a small client for the vde extraction service, using URLSession,
// async/await and a hand-assembled multipart body. No dependencies beyond Foundation.
//
// WHERE THIS MAY RUN
// The bearer token is a service credential. Use this client only
//   - in server-side Swift (Vapor, Hummingbird, a command-line tool or a worker), or
//   - in code that runs on your own backend, with the token read from the server's
//     environment or secret store.
// An app on a user's device must NEVER contain the token: it would be extractable from the
// binary or from traffic. A shipped app sends the document to its own backend (with the
// user's own authentication), and that backend uses this client or an equivalent.
//
// On Linux, import FoundationNetworking as well (the conditional import below does it).
//
// Usage:
//   let client = VDEClient(baseURL: URL(string: "https://extract.example.com")!, token: token)
//   let job = try await client.extract(
//       VDEClient.Submission(
//           document: .init(data: bytes, mediaType: "text/plain; charset=utf-8", filename: "booking.txt"),
//           extractorInstructions: extractorMD, judgeInstructions: judgeMD, schema: schemaJSON),
//       idempotencyKey: key)
//   if job.status == "accepted" { /* use job.result */ }

import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

public struct VDEClient: Sendable {
    public let baseURL: URL
    private let token: String
    private let session: URLSession

    public init(baseURL: URL, token: String, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    // MARK: Types

    public struct Part: Sendable {
        public var data: Data
        public var mediaType: String
        public var filename: String
        public init(data: Data, mediaType: String, filename: String = "document") {
            self.data = data
            self.mediaType = mediaType
            self.filename = filename
        }
    }

    public struct Submission: Sendable {
        public var document: Part
        /// Rendered pages (PNG or JPEG), page 1 first. Empty for text, HTML and single images.
        public var pages: [Part] = []
        public var extractorInstructions: String
        public var judgeInstructions: String
        /// JSON Schema 2020-12 as text. nil runs the job schema-less.
        public var schema: String?
        /// "none" deletes the content on the first read of the terminal state.
        public var retention: String = "none"
        public var maxAttempts: Int?

        public init(document: Part, pages: [Part] = [], extractorInstructions: String,
                    judgeInstructions: String, schema: String? = nil,
                    retention: String = "none", maxAttempts: Int? = nil) {
            self.document = document
            self.pages = pages
            self.extractorInstructions = extractorInstructions
            self.judgeInstructions = judgeInstructions
            self.schema = schema
            self.retention = retention
            self.maxAttempts = maxAttempts
        }
    }

    public struct FeedbackEntry: Decodable, Sendable {
        public let pointer: String
        public let message: String
        public let source: String?
    }

    public struct JobError: Decodable, Sendable {
        public let code: String
        public let message: String
    }

    public struct Job: Decodable, Sendable {
        public let id: String
        public let status: String  // queued, running, accepted, rejected, failed (or a later one)
        public let result: JSONValue?
        public let feedback: [FeedbackEntry]
        public let attempts: Int
        public let serviceVersion: String
        public let judgeConfidence: Double?
        public let error: JobError?
        public let expiresAt: String

        public var isTerminal: Bool { ["accepted", "rejected", "failed"].contains(status) }

        enum CodingKeys: String, CodingKey {
            case id, status, result, feedback, attempts, error
            case serviceVersion = "service_version"
            case judgeConfidence = "judge_confidence"
            case expiresAt = "expires_at"
        }
    }

    public struct APIError: Error, CustomStringConvertible, Sendable {
        public let status: Int
        public let code: String?
        public let message: String?
        public let requestID: String?
        public let retryAfter: String?
        public let quotaRemaining: Int?

        /// 429 and 5xx are worth retrying; 400, 413, 415, 422 mean the request is wrong,
        /// and 401, 403, 404 need a configuration fix or mean the job is gone.
        public var isRetryable: Bool { status == 429 || status >= 500 }
        public var description: String { "\(status) \(code ?? "error"): \(message ?? "")" }
    }

    /// Any JSON value, so `result` can be decoded whatever the schema is.
    public enum JSONValue: Decodable, Sendable {
        case null, bool(Bool), number(Double), string(String), array([JSONValue]), object([String: JSONValue])

        public init(from decoder: Decoder) throws {
            let c = try decoder.singleValueContainer()
            if c.decodeNil() { self = .null }
            else if let v = try? c.decode(Bool.self) { self = .bool(v) }
            else if let v = try? c.decode(Double.self) { self = .number(v) }
            else if let v = try? c.decode(String.self) { self = .string(v) }
            else if let v = try? c.decode([JSONValue].self) { self = .array(v) }
            else { self = .object(try c.decode([String: JSONValue].self)) }
        }
    }

    // MARK: Calls

    /// Submits a document. Returns the job, status "queued".
    public func submit(_ s: Submission, idempotencyKey: String? = nil) async throws -> Job {
        var form = MultipartForm()
        form.addFile(name: "document", filename: s.document.filename, mediaType: s.document.mediaType, data: s.document.data)
        for page in s.pages {
            form.addFile(name: "page", filename: page.filename, mediaType: page.mediaType, data: page.data)
        }
        form.addFile(name: "extractor_instructions", filename: "extractor.md", mediaType: "text/markdown", data: Data(s.extractorInstructions.utf8))
        form.addFile(name: "judge_instructions", filename: "judge.md", mediaType: "text/markdown", data: Data(s.judgeInstructions.utf8))
        if let schema = s.schema {
            form.addFile(name: "schema", filename: "schema.json", mediaType: "application/schema+json", data: Data(schema.utf8))
        }
        form.addField(name: "retention", value: s.retention)
        if let n = s.maxAttempts { form.addField(name: "max_attempts", value: String(n)) }

        var req = request("POST", "v1/jobs")
        req.setValue("multipart/form-data; boundary=\(form.boundary)", forHTTPHeaderField: "Content-Type")
        if let key = idempotencyKey { req.setValue(key, forHTTPHeaderField: "Idempotency-Key") }
        req.httpBody = form.finish()
        return try await send(req)
    }

    public func job(id: String) async throws -> Job {
        try await send(request("GET", "v1/jobs/\(id)"))
    }

    /// Polls from 2 s, doubling to 30 s, until the job is terminal. A poll failing with 429,
    /// 5xx or a transport error is repeated; the third failure in a row is thrown. With
    /// retention "none" the terminal read is the only one, so store what it returns.
    public func wait(for id: String, timeout: TimeInterval = 600) async throws -> Job {
        let deadline = Date().addingTimeInterval(timeout)
        var delay: Double = 2
        var failures = 0
        while true {
            try await Task.sleep(nanoseconds: UInt64(delay * Double.random(in: 1.0...1.1) * 1_000_000_000))
            do {
                let j = try await job(id: id)
                failures = 0
                if j.isTerminal { return j }
            } catch {
                if let api = error as? APIError, !api.isRetryable { throw api }
                failures += 1
                if failures >= 3 { throw error }
            }
            if Date() > deadline { throw URLError(.timedOut) }
            delay = min(delay * 2, 30)
        }
    }

    /// Submit and wait. Use a stable key (a hash of the document and the instruction and
    /// schema versions) so a retry after a lost response cannot run the extraction twice.
    public func extract(_ s: Submission, idempotencyKey: String? = nil) async throws -> Job {
        let job = try await submit(s, idempotencyKey: idempotencyKey)
        return try await wait(for: job.id)
    }

    // MARK: Plumbing

    private func request(_ method: String, _ path: String) -> URLRequest {
        var req = URLRequest(url: baseURL.appendingPathComponent(path))
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return req
    }

    private func send(_ req: URLRequest) async throws -> Job {
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        guard (200..<300).contains(http.statusCode) else {
            struct Body: Decodable {
                struct E: Decodable { let code: String?; let message: String?; let request_id: String? }
                let error: E?
            }
            let body = try? JSONDecoder().decode(Body.self, from: data)
            throw APIError(
                status: http.statusCode, code: body?.error?.code, message: body?.error?.message,
                requestID: body?.error?.request_id ?? http.value(forHTTPHeaderField: "X-Request-Id"),
                retryAfter: http.value(forHTTPHeaderField: "Retry-After"),
                quotaRemaining: http.value(forHTTPHeaderField: "X-Quota-Remaining").flatMap(Int.init))
        }
        return try JSONDecoder().decode(Job.self, from: data)
    }
}

/// A multipart/form-data body assembled by hand.
struct MultipartForm {
    let boundary = "vde-" + UUID().uuidString
    private var body = Data()

    private mutating func append(_ s: String) { body.append(Data(s.utf8)) }

    private func quoted(_ s: String) -> String {
        s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            .replacingOccurrences(of: "\r", with: "").replacingOccurrences(of: "\n", with: "")
    }

    mutating func addFile(name: String, filename: String, mediaType: String, data: Data) {
        append("--\(boundary)\r\n")
        append("Content-Disposition: form-data; name=\"\(name)\"; filename=\"\(quoted(filename))\"\r\n")
        append("Content-Type: \(mediaType)\r\n\r\n")
        body.append(data)
        append("\r\n")
    }

    mutating func addField(name: String, value: String) {
        append("--\(boundary)\r\n")
        append("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n")
        append("\(value)\r\n")
    }

    mutating func finish() -> Data {
        append("--\(boundary)--\r\n")
        return body
    }
}
