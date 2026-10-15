import Foundation

/// Invalidates in-flight status observations whenever a lifecycle intent changes.
public struct ServiceLifecycleObservationRevision: Sendable {
    public private(set) var value: Int
    public private(set) var serviceExpectedToRun: Bool
    public private(set) var absenceSuppressed: Bool
    private var pendingIntent: Intent?
    private var suppressionBeforeLifecycle: Bool?

    private enum Intent: Sendable, Equatable {
        case lifecycle
        case start
    }

    public init(value: Int = 0, serviceExpectedToRun: Bool = false, absenceSuppressed: Bool = false) {
        self.value = value
        self.serviceExpectedToRun = serviceExpectedToRun
        self.absenceSuppressed = absenceSuppressed
    }

    @discardableResult
    public mutating func beginLifecycleIntent(operation: String) -> Int {
        value += 1
        pendingIntent = .lifecycle
        suppressionBeforeLifecycle = absenceSuppressed
        if operation == "stop" { absenceSuppressed = true }
        return value
    }

    @discardableResult
    public mutating func beginStartIntent() -> Int {
        value += 1
        pendingIntent = .start
        suppressionBeforeLifecycle = nil
        serviceExpectedToRun = true
        absenceSuppressed = false
        return value
    }

    public mutating func observeLifecycle(_ lifecycle: String) {
        if lifecycle == "running" || lifecycle == "paused" {
            serviceExpectedToRun = true
        } else if lifecycle == "stopped" {
            serviceExpectedToRun = false
        }
    }

    public mutating func beginUpdaterQuiescence() -> Int {
        value += 1
        pendingIntent = nil
        suppressionBeforeLifecycle = nil
        absenceSuppressed = true
        return value
    }

    public mutating func finishUpdaterRecovery() -> Int {
        value += 1
        pendingIntent = nil
        suppressionBeforeLifecycle = nil
        absenceSuppressed = false
        return value
    }

    public mutating func finishLifecycle(_ observation: Int, operation: String, succeeded: Bool) -> Bool {
        guard finishLifecycle(observation) else { return false }
        if operation == "stop", succeeded {
            serviceExpectedToRun = false
        } else if operation == "stop", !succeeded {
            absenceSuppressed = suppressionBeforeLifecycle ?? false
        }
        suppressionBeforeLifecycle = nil
        return true
    }

    public mutating func finishStartFailure(_ observation: Int) -> Bool {
        guard finishStart(observation) else { return false }
        serviceExpectedToRun = true
        return true
    }

    public mutating func synchronize(to revision: Int, serviceExpectedToRun: Bool, absenceSuppressed: Bool) {
        guard revision != value || serviceExpectedToRun != self.serviceExpectedToRun || absenceSuppressed != self.absenceSuppressed else { return }
        value = revision
        self.serviceExpectedToRun = serviceExpectedToRun
        self.absenceSuppressed = absenceSuppressed
        pendingIntent = nil
        suppressionBeforeLifecycle = nil
    }

    public func accepts(_ observation: Int) -> Bool {
        observation == value && pendingIntent == nil
    }

    public func acceptsStartStatus(_ observation: Int) -> Bool {
        observation == value && pendingIntent == .start
    }

    public func acceptsLifecycleCompletion(_ observation: Int) -> Bool {
        observation == value && pendingIntent == .lifecycle
    }

    @discardableResult
    public mutating func finishStart(_ observation: Int) -> Bool {
        guard acceptsStartStatus(observation) else { return false }
        pendingIntent = nil
        return true
    }

    @discardableResult
    public mutating func finishLifecycle(_ observation: Int) -> Bool {
        guard acceptsLifecycleCompletion(observation) else { return false }
        pendingIntent = nil
        return true
    }

    public var canRefresh: Bool { pendingIntent == nil }

    public var isStartPending: Bool { pendingIntent == .start }
}
