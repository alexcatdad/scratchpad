import Foundation

public enum QueueKeyIdentifier {
    public static let captureQueue = "com.alexcatdad.scratchpad.capture-queue.v1"
}

public struct KeychainHelperArguments: Equatable, Sendable {
    public enum Operation: String, Equatable, Sendable {
        case get
        case create
    }

    public let operation: Operation
    public let identifier: String

    public enum CreateFailureDisposition: Equatable, Sendable {
        case loadExisting
        case fail
    }

    public static func createFailureDisposition(isDuplicateItem: Bool) -> CreateFailureDisposition {
        isDuplicateItem ? .loadExisting : .fail
    }

    /// Parses the two arguments after the executable name. The helper is
    /// intentionally scoped to the one queue key it owns.
    public init?(arguments: [String]) {
        guard arguments.count == 2,
              let operation = Operation(rawValue: arguments[0]),
              arguments[1] == QueueKeyIdentifier.captureQueue
        else { return nil }

        self.operation = operation
        self.identifier = arguments[1]
    }
}
