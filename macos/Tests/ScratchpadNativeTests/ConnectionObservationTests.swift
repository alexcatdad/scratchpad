import Foundation
import Testing
@testable import ScratchpadNative

@Test func staleOrUnparseableConnectionObservationsAreUnknown() {
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    let formatter = ISO8601DateFormatter()
    let stale = ConnectionObservation(state: "connected", checkedAt: formatter.string(from: now.addingTimeInterval(-61)))
    let malformed = ConnectionObservation(state: "connected", checkedAt: "not-a-date")
    let missing = ConnectionObservation(state: "connected", checkedAt: nil)
    let current = ConnectionObservation(state: "connected", checkedAt: formatter.string(from: now.addingTimeInterval(-30)))

    #expect(stale.effectiveState(at: now) == "unknown")
    #expect(malformed.effectiveState(at: now) == "unknown")
    #expect(missing.effectiveState(at: now) == "unknown")
    #expect(current.effectiveState(at: now) == "connected")
}
