import Foundation

/// A feed item must declare the queue-schema versions it can read. These
/// values are public custom appcast properties, not inferred from the bundle
/// version or undocumented Sparkle cache state.
public enum UpdateCompatibility {
    public static let minimumKey = "scratchpad:minReadableQueueSchema"
    public static let maximumKey = "scratchpad:maxReadableQueueSchema"

    public static func isCompatible(properties: [AnyHashable: Any], installedSchema: Int) -> Bool {
        readableRange(properties: properties)?.contains(installedSchema) ?? false
    }

    public static func readableRange(properties: [AnyHashable: Any]) -> QueueSchemaRange? {
        guard let minimum = integer(properties[minimumKey]),
              let maximum = integer(properties[maximumKey]),
              minimum > 0, maximum >= minimum else { return nil }
        return QueueSchemaRange(minimum: minimum, maximum: maximum)
    }

    private static func integer(_ value: Any?) -> Int? {
        if let number = value as? NSNumber { return number.intValue }
        if let string = value as? String { return Int(string) }
        return nil
    }
}

public struct QueueSchemaRange: Equatable, Sendable {
    public let minimum: Int
    public let maximum: Int
    public init(minimum: Int, maximum: Int) { self.minimum = minimum; self.maximum = maximum }
    public func contains(_ schema: Int) -> Bool { schema > 0 && (minimum...maximum).contains(schema) }
}
