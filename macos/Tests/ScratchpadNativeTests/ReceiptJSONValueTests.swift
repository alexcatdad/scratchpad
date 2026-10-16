import Foundation
import Testing
@testable import ScratchpadNative

@Test func receiptPayloadPreservesNestedJSONAndNumericValues() throws {
    let data = Data(#"{"count":3,"ratio":1.25,"enabled":true,"items":[null,"x"]}"#.utf8)
    let decoded = try JSONDecoder().decode(JSONValue.self, from: data)

    guard case .object(let object) = decoded else { Issue.record("Expected a JSON object"); return }
    #expect(object["count"] == .number(3))
    #expect(object["ratio"] == .number(Decimal(string: "1.25")!))
    #expect(object["enabled"] == .bool(true))
    #expect(object["items"] == .array([.null, .string("x")]))

    let reencoded = try JSONEncoder().encode(decoded)
    #expect(try JSONDecoder().decode(JSONValue.self, from: reencoded) == decoded)
}
