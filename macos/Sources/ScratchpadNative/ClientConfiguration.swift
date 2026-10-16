import Foundation
import Darwin

public enum MCPClientKind: String, CaseIterable, Identifiable, Sendable {
    case codex = "Codex"
    case claudeCode = "Claude Code"
    case generic = "Generic stdio"
    public var id: String { rawValue }
}

public struct ClientConfigPreview: Sendable {
    public let path: String
    public let before: String
    public let after: String
    public var changed: Bool { before != after }
}

/// The exact file state that was shown to the owner for approval. Apply is a
/// compare-and-swap against both the selected path and these original bytes.
public struct ReviewedConfigFile: Sendable {
    public let path: String
    public let originalContents: Data?
    public let proposedContents: Data

    public init(path: String, originalContents: Data?, proposedContents: Data) {
        self.path = URL(fileURLWithPath: path).standardizedFileURL.path
        self.originalContents = originalContents
        self.proposedContents = proposedContents
    }
}

/// Pure preview generation. It edits only Scratchpad's entry and keeps unrelated
/// client settings. Callers show this exact result before applying it.
public enum ClientConfiguration {
    public static func readConfigFile(at url: URL) throws -> Data? {
        let path = url.standardizedFileURL.path
        switch try inspectConfigFile(path: path) {
        case .missing: return nil
        case .regular: return try Data(contentsOf: URL(fileURLWithPath: path))
        case .other: throw ClientConfigurationError.notRegularFile
        case .symbolicLink: throw ClientConfigurationError.symbolicLink
        }
    }

    public static func applyReviewedFile(_ preview: ReviewedConfigFile, selectedURL: URL) throws {
        let selectedPath = selectedURL.standardizedFileURL.path
        guard selectedPath == preview.path else { throw ClientConfigurationError.targetChanged }

        let current: Data?
        switch try inspectConfigFile(path: selectedPath) {
        case .missing: current = nil
        case .regular: current = try Data(contentsOf: URL(fileURLWithPath: selectedPath))
        case .other: throw ClientConfigurationError.notRegularFile
        case .symbolicLink: throw ClientConfigurationError.symbolicLink
        }
        guard current == preview.originalContents else { throw ClientConfigurationError.contentsChanged }
        try writeAtomically(preview.proposedContents, to: URL(fileURLWithPath: selectedPath))
    }

    public static func codexPreview(existing: String, executable: String, configuration: ServiceConfiguration) throws -> String {
        guard !existing.contains("\"\"\""), !existing.contains("'''") else {
            throw ClientConfigurationError.unsupportedTOMLSyntax
        }
        var lines = existing.components(separatedBy: .newlines)
        if lines.last == "" { lines.removeLast() }

        let sections = try parseTOMLSections(lines)
        let target = sections.filter { $0.path == ["mcp_servers", "scratchpad"] }
        let environment = sections.filter { $0.path == ["mcp_servers", "scratchpad", "env"] }
        guard target.count <= 1, environment.count <= 1 else { throw ClientConfigurationError.unsupportedTOMLSyntax }
        if target.isEmpty, !environment.isEmpty { throw ClientConfigurationError.unsupportedTOMLSyntax }
        try validateDottedAssignments(lines, sections: sections)

        if target.isEmpty {
            appendTOMLSection("[mcp_servers.scratchpad]", to: &lines)
        }
        var reparsed = try parseTOMLSections(lines)
        guard let targetSection = reparsed.first(where: { $0.path == ["mcp_servers", "scratchpad"] }) else {
            throw ClientConfigurationError.unsupportedTOMLSyntax
        }
        try validateAssignments(in: targetSection, lines: lines)
        if assignmentLine("env", in: targetSection, lines: lines) != nil {
            throw ClientConfigurationError.unsupportedTOMLSyntax
        }
        if let args = assignmentLine("args", in: targetSection, lines: lines) {
            let value = String(stripTOMLComment(lines[args]).split(separator: "=", maxSplits: 1).last ?? "").trimmingCharacters(in: .whitespaces)
            guard hasBalancedInlineCollections(value) else { throw ClientConfigurationError.unsupportedTOMLSyntax }
            lines.remove(at: args)
        }
        reparsed = try parseTOMLSections(lines)
        guard let currentTarget = reparsed.first(where: { $0.path == ["mcp_servers", "scratchpad"] }) else {
            throw ClientConfigurationError.unsupportedTOMLSyntax
        }
        if let command = assignmentLine("command", in: currentTarget, lines: lines) {
            lines[command] = replacingTOMLAssignment(lines[command], key: "command", value: executable)
        } else {
            lines.insert("command = \"\(tomlEscape(executable))\"", at: currentTarget.end)
        }

        reparsed = try parseTOMLSections(lines)
        let envSection = reparsed.first(where: { $0.path == ["mcp_servers", "scratchpad", "env"] })
        if let envSection {
            try validateAssignments(in: envSection, lines: lines)
            for key in connectionEnvironmentKeys {
                if let index = assignmentLine(key, in: envSection, lines: lines) {
                    lines[index] = ""
                }
            }
            if lines[(envSection.header + 1)..<envSection.end].allSatisfy({ stripTOMLComment($0).trimmingCharacters(in: .whitespaces).isEmpty }) {
                lines.removeSubrange(envSection.header..<envSection.end)
            }
        }
        return lines.joined(separator: "\n") + "\n"
    }

    public static func jsonPreview(existing: Data, executable: String, configuration: ServiceConfiguration) throws -> Data {
        var root: [String: Any]
        if existing.isEmpty { root = [:] }
        else {
            guard let parsed = try JSONSerialization.jsonObject(with: existing) as? [String: Any] else { throw ClientConfigurationError.invalidJSONRoot }
            root = parsed
        }
        var servers: [String: Any]
        if let existingServers = root["mcpServers"] {
            guard let dictionary = existingServers as? [String: Any] else { throw ClientConfigurationError.invalidMCPServers }
            servers = dictionary
        } else { servers = [:] }
        var entry: [String: Any]
        if let current = servers["scratchpad"] {
            guard let dictionary = current as? [String: Any] else { throw ClientConfigurationError.invalidScratchpadEntry }
            entry = dictionary
        } else { entry = [:] }
        var environment: [String: String]
        if let current = entry["env"] {
            guard let dictionary = current as? [String: String] else { throw ClientConfigurationError.invalidEntryEnvironment }
            environment = dictionary
        } else { environment = [:] }
        connectionEnvironmentKeys.forEach { environment.removeValue(forKey: $0) }
        entry["command"] = executable
        entry.removeValue(forKey: "args")
        if environment.isEmpty { entry.removeValue(forKey: "env") }
        else { entry["env"] = environment }
        servers["scratchpad"] = entry
        root["mcpServers"] = servers
        return try JSONSerialization.data(withJSONObject: root, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]) + Data([0x0A])
    }

    public static func genericEntry(executable: String, configuration: ServiceConfiguration) throws -> Data {
        try JSONSerialization.data(withJSONObject: ["command": executable], options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]) + Data([0x0A])
    }

    public static func writeAtomically(_ bytes: Data, to url: URL) throws {
        let manager = FileManager.default
        let path = url.standardizedFileURL.path
        let existingMode: Int?
        switch try inspectConfigFile(path: path) {
        case .missing: existingMode = nil
        case .regular:
            existingMode = (try? manager.attributesOfItem(atPath: path)[.posixPermissions] as? NSNumber)?.intValue
        case .other: throw ClientConfigurationError.notRegularFile
        case .symbolicLink: throw ClientConfigurationError.symbolicLink
        }
        let directory = url.deletingLastPathComponent()
        try manager.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        try bytes.write(to: URL(fileURLWithPath: path), options: .atomic)
        try manager.setAttributes([.posixPermissions: existingMode ?? 0o600], ofItemAtPath: path)
    }

    private enum ConfigFileKind { case missing, regular, other, symbolicLink }
    private static func inspectConfigFile(path: String) throws -> ConfigFileKind {
        var info = stat()
        if lstat(path, &info) == 0 {
            let kind = info.st_mode & S_IFMT
            if kind == S_IFLNK { return .symbolicLink }
            if kind == S_IFREG { return .regular }
            return .other
        }
        if errno == ENOENT { return .missing }
        throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
    }

    private static let connectionEnvironmentKeys = ["SCRATCHPAD_URL", "SCRATCHPAD_PUBLIC_KEY", "SCRATCHPAD_SIGNING_KEY"]

    private static func tomlEscape(_ value: String) -> String {
        value.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
    }

    private struct TOMLSection {
        let path: [String]
        let header: Int
        let end: Int
    }

    private static func parseTOMLSections(_ lines: [String]) throws -> [TOMLSection] {
        var found: [(path: [String], header: Int)] = []
        for (index, line) in lines.enumerated() {
            let content = stripTOMLComment(line).trimmingCharacters(in: .whitespaces)
            guard content.first == "[" else { continue }
            let arrayTable = content.hasPrefix("[[")
            let inner: String
            if arrayTable {
                guard content.hasSuffix("]]"), content.count > 4 else { throw ClientConfigurationError.unsupportedTOMLSyntax }
                inner = String(content.dropFirst(2).dropLast(2))
            } else {
                guard content.last == "]" else { throw ClientConfigurationError.unsupportedTOMLSyntax }
                inner = String(content.dropFirst().dropLast())
            }
            if inner.contains("\\") && (inner.contains("mcp_servers") || inner.localizedCaseInsensitiveContains("scratchpad")) {
                throw ClientConfigurationError.unsupportedTOMLSyntax
            }
            let path = try parseTOMLPath(inner)
            if path.count >= 2, path[0] == "mcp_servers", path[1].localizedCaseInsensitiveContains("scratchpad"),
               arrayTable || (path != ["mcp_servers", "scratchpad"] && path != ["mcp_servers", "scratchpad", "env"]) {
                throw ClientConfigurationError.unsupportedTOMLSyntax
            }
            if path.count >= 2, path[0] == "mcp_servers", path[1].contains("\\") {
                throw ClientConfigurationError.unsupportedTOMLSyntax
            }
            found.append((path, index))
        }
        return found.enumerated().map { offset, section in
            TOMLSection(path: section.path, header: section.header,
                        end: offset + 1 < found.count ? found[offset + 1].header : lines.count)
        }
    }

    private static func parseTOMLPath(_ text: String) throws -> [String] {
        var parts: [String] = []
        var token = ""
        var quoteCharacter: Character?
        var escaped = false
        for character in text {
            if let quote = quoteCharacter {
                if quote == "\"", escaped { token.append(character); escaped = false }
                else if quote == "\"", character == "\\" { escaped = true }
                else if character == quote { quoteCharacter = nil }
                else { token.append(character) }
            } else if character == "\"" || character == "'" {
                guard token.trimmingCharacters(in: .whitespaces).isEmpty else { throw ClientConfigurationError.unsupportedTOMLSyntax }
                token = ""
                quoteCharacter = character
            } else if character == "." {
                let part = token.trimmingCharacters(in: .whitespaces)
                guard !part.isEmpty else { throw ClientConfigurationError.unsupportedTOMLSyntax }
                parts.append(part); token = ""
            } else { token.append(character) }
        }
        let part = token.trimmingCharacters(in: .whitespaces)
        guard quoteCharacter == nil, !part.isEmpty else { throw ClientConfigurationError.unsupportedTOMLSyntax }
        parts.append(part)
        return parts
    }

    private static func stripTOMLComment(_ line: String) -> String {
        var quote: Character?
        var escaped = false
        for (index, character) in line.enumerated() {
            if let currentQuote = quote {
                if currentQuote == "\"", escaped { escaped = false }
                else if currentQuote == "\"", character == "\\" { escaped = true }
                else if character == currentQuote { quote = nil }
            } else if character == "\"" || character == "'" { quote = character }
            else if character == "#" { return String(line.prefix(index)) }
        }
        return line
    }

    private static func assignmentLine(_ key: String, in section: TOMLSection, lines: [String]) -> Int? {
        guard section.header + 1 < section.end else { return nil }
        for index in (section.header + 1)..<section.end {
            let content = stripTOMLComment(lines[index])
            guard let equal = content.firstIndex(of: "=") else { continue }
            if parseTOMLKey(content[..<equal].trimmingCharacters(in: .whitespaces)) == key { return index }
        }
        return nil
    }

    private static func validateAssignments(in section: TOMLSection, lines: [String]) throws {
        var keys = Set<String>()
        if section.header + 1 < section.end {
            for line in lines[(section.header + 1)..<section.end] {
                let content = stripTOMLComment(line).trimmingCharacters(in: .whitespaces)
                guard let equal = content.firstIndex(of: "=") else { continue }
                let rawKey = content[..<equal].trimmingCharacters(in: .whitespaces)
                guard !rawKey.contains(".") else { throw ClientConfigurationError.unsupportedTOMLSyntax }
                let key = parseTOMLKey(rawKey)
                if let key, ["command", "args", "env", "SCRATCHPAD_URL", "SCRATCHPAD_PUBLIC_KEY", "SCRATCHPAD_SIGNING_KEY"].contains(key), !keys.insert(key).inserted {
                    throw ClientConfigurationError.unsupportedTOMLSyntax
                }
                if let key, ["command", "args", "env", "SCRATCHPAD_URL", "SCRATCHPAD_PUBLIC_KEY", "SCRATCHPAD_SIGNING_KEY"].contains(key) {
                    let value = content[content.index(after: equal)...].trimmingCharacters(in: .whitespaces)
                    if value.contains("\"\"\""), value.components(separatedBy: "\"\"\"").count < 3 { throw ClientConfigurationError.unsupportedTOMLSyntax }
                    if value.contains("'''") { throw ClientConfigurationError.unsupportedTOMLSyntax }
                }
            }
        }
    }

    private static func validateDottedAssignments(_ lines: [String], sections: [TOMLSection]) throws {
        let headers = Dictionary(uniqueKeysWithValues: sections.map { ($0.header, $0.path) })
        var currentPath: [String] = []
        for (index, line) in lines.enumerated() {
            if let path = headers[index] { currentPath = path; continue }
            let content = stripTOMLComment(line).trimmingCharacters(in: .whitespaces)
            guard let equal = content.firstIndex(of: "=") else { continue }
            let rawKey = content[..<equal].trimmingCharacters(in: .whitespaces)
            if currentPath == ["mcp_servers"] && (parseTOMLKey(rawKey) == "scratchpad" || rawKey.contains("\\")) {
                throw ClientConfigurationError.unsupportedTOMLSyntax
            }
            if currentPath == ["mcp_servers", "scratchpad"] || currentPath == ["mcp_servers", "scratchpad", "env"] {
                if rawKey.contains(".") { throw ClientConfigurationError.unsupportedTOMLSyntax }
            } else if currentPath.isEmpty {
                if rawKey.hasPrefix("mcp_servers.") && rawKey.contains("\\") { throw ClientConfigurationError.unsupportedTOMLSyntax }
                let keyPath = try? parseTOMLPath(rawKey)
                if keyPath?.starts(with: ["mcp_servers", "scratchpad"]) == true { throw ClientConfigurationError.unsupportedTOMLSyntax }
                if parseTOMLKey(rawKey) == "mcp_servers" { throw ClientConfigurationError.unsupportedTOMLSyntax }
            }
        }
    }

    private static func hasBalancedInlineCollections(_ value: String) -> Bool {
        var square = 0
        var curly = 0
        var quote: Character?
        var escaped = false
        for character in value {
            if let currentQuote = quote {
                if currentQuote == "\"", escaped { escaped = false }
                else if currentQuote == "\"", character == "\\" { escaped = true }
                else if character == currentQuote { quote = nil }
            } else if character == "\"" || character == "'" { quote = character }
            else if character == "[" { square += 1 }
            else if character == "]" { square -= 1 }
            else if character == "{" { curly += 1 }
            else if character == "}" { curly -= 1 }
            if square < 0 || curly < 0 { return false }
        }
        return quote == nil && square == 0 && curly == 0
    }

    private static func parseTOMLKey(_ source: String) -> String? {
        let key = source.trimmingCharacters(in: .whitespaces)
        if key.first == "\"", key.last == "\"", key.count >= 2 {
            return String(key.dropFirst().dropLast())
        }
        if key.first == "'", key.last == "'", key.count >= 2 {
            return String(key.dropFirst().dropLast())
        }
        guard !key.isEmpty, key.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "_" || $0 == "-" }) else { return nil }
        return String(key)
    }

    private static func replacingTOMLAssignment(_ line: String, key: String, value: String) -> String {
        let comment = inlineTOMLComment(in: line)
        let prefix = String(line.prefix { $0 == " " || $0 == "\t" })
        return prefix + "\(key) = \"\(tomlEscape(value))\"" + comment
    }

    private static func inlineTOMLComment(in line: String) -> String {
        let content = stripTOMLComment(line)
        guard content.count < line.count else { return "" }
        return " " + line.dropFirst(content.count).trimmingCharacters(in: .whitespaces)
    }

    private static func appendTOMLSection(_ header: String, to lines: inout [String]) {
        while lines.last == "" { lines.removeLast() }
        if !lines.isEmpty { lines.append("") }
        lines.append(header)
    }
}

public enum ClientConfigurationError: Error, LocalizedError {
    case invalidJSONRoot, invalidMCPServers, invalidScratchpadEntry, invalidEntryEnvironment, unsupportedTOMLSyntax
    case targetChanged, contentsChanged, notRegularFile, symbolicLink
    public var errorDescription: String? {
        switch self {
        case .invalidJSONRoot: "The selected configuration must contain a JSON object."
        case .invalidMCPServers: "The existing mcpServers setting is not an object; it was left unchanged."
        case .invalidScratchpadEntry: "The existing Scratchpad entry is not an object; it was left unchanged."
        case .invalidEntryEnvironment: "The existing Scratchpad environment is not a string map; it was left unchanged."
        case .unsupportedTOMLSyntax: "The existing Codex TOML uses unsupported or ambiguous Scratchpad syntax; it was left unchanged."
        case .targetChanged: "The selected configuration file changed after preview. Review a fresh preview for the current file."
        case .contentsChanged: "The configuration file changed after preview. Review a fresh preview before applying."
        case .notRegularFile: "The selected configuration path is not a regular file."
        case .symbolicLink: "The selected configuration file is a symbolic link. Choose the resolved file path and review it directly."
        }
    }
}
