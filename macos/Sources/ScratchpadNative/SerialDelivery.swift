/// Delivers values one at a time, in submission order, from a single
/// long-lived task.
///
/// Each poll used to start its own unstructured `Task` to post incident
/// notifications. Those tasks could interleave while awaiting notification
/// permission, so "recovered" could post before "offline". Every value
/// submitted here is handled by one consumer, and the next value waits until
/// the previous `deliver` call returns.
public final class SerialDelivery<Value: Sendable>: Sendable {
    private let continuation: AsyncStream<Value>.Continuation
    private let consumer: Task<Void, Never>

    public init(deliver: @escaping @Sendable (Value) async -> Void) {
        let (stream, continuation) = AsyncStream<Value>.makeStream(bufferingPolicy: .unbounded)
        self.continuation = continuation
        consumer = Task {
            for await value in stream { await deliver(value) }
        }
    }

    deinit {
        continuation.finish()
    }

    /// Queues values for delivery after everything submitted before them.
    public func submit(_ values: [Value]) {
        for value in values { continuation.yield(value) }
    }

    /// Stops accepting values and waits for the queued ones to be delivered.
    public func finish() async {
        continuation.finish()
        await consumer.value
    }
}
