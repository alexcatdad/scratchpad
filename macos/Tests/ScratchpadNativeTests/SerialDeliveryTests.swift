import Foundation
import Testing

@testable import ScratchpadNative

private actor DeliveryLog {
    private(set) var entries: [String] = []
    private(set) var concurrent = 0
    private(set) var maximumConcurrent = 0

    func begin() {
        concurrent += 1
        maximumConcurrent = max(maximumConcurrent, concurrent)
    }

    func end(_ entry: String) {
        entries.append(entry)
        concurrent -= 1
    }
}

@Test func serialDeliveryKeepsSubmissionOrderAcrossPolls() async {
    let log = DeliveryLog()
    // The first value takes longest, as a permission prompt would: later polls
    // must still wait for it instead of overtaking it.
    let delays: [String: UInt64] = ["offline": 50_000_000, "localSavingUnavailable": 20_000_000, "recovered": 0]
    let delivery = SerialDelivery<String> { value in
        await log.begin()
        try? await Task.sleep(nanoseconds: delays[value] ?? 0)
        await log.end(value)
    }

    delivery.submit(["offline"])
    delivery.submit(["localSavingUnavailable"])
    delivery.submit([])
    delivery.submit(["recovered"])
    await delivery.finish()

    #expect(await log.entries == ["offline", "localSavingUnavailable", "recovered"])
    #expect(await log.maximumConcurrent == 1)
}

@Test func serialDeliveryIgnoresValuesAfterFinish() async {
    let log = DeliveryLog()
    let delivery = SerialDelivery<String> { value in
        await log.begin()
        await log.end(value)
    }
    delivery.submit(["first"])
    await delivery.finish()
    delivery.submit(["late"])

    #expect(await log.entries == ["first"])
}
