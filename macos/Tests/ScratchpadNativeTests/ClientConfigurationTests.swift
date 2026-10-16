import Foundation
import Testing
@testable import ScratchpadNative

private let testConfiguration = ServiceConfiguration(
    serverURL: "https://memory.example.test",
    credentialFingerprint: "SHA256:synthetic",
    publicKeyPath: "/tmp/synthetic.pub",
    signingKeyPath: "/tmp/synthetic-key"
)

@Test func codexPreviewPreservesOtherTablesAndMirrorPreferences() throws {
    let existing = """
    model = "example"
    [mcp_servers.other]
    command = "other-tool"
    [mcp_servers.scratchpad]
    command = "old"
    [mcp_servers.scratchpad.env]
    SCRATCHPAD_MIRROR = "true"
    SCRATCHPAD_MIRROR_PATH = "/tmp/synthetic-mirror"
    [ui]
    theme = "dark"
    """
    let result = try ClientConfiguration.codexPreview(existing: existing, executable: "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp", configuration: testConfiguration)
    #expect(result.contains("model = \"example\""))
    #expect(result.contains("[mcp_servers.other]"))
    #expect(result.contains("theme = \"dark\""))
    #expect(result.contains("SCRATCHPAD_MIRROR = \"true\""))
    #expect(!result.contains("SCRATCHPAD_URL"))
    #expect(result.contains("command = \"/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp\""))
    #expect(!result.contains("command = \"old\""))
}

@Test func codexPreviewUpdatesQuotedCommentedTablesInPlace() throws {
    let existing = """
    model = "example"
    [mcp_servers."scratchpad"] # quoted table with legal comment
    command = "old" # replace command only
    args = ["--stdio"]
    enabled = true
    startup_timeout_ms = 9000
    [mcp_servers."scratchpad".env] # env settings
    SCRATCHPAD_MIRROR = "true" # preserve mirror policy
    SCRATCHPAD_MIRROR_PATH = "/tmp/synthetic-mirror"
    KEEP = "value"
    [ui]
    theme = "dark"
    """
    let result = try ClientConfiguration.codexPreview(existing: existing, executable: "/tmp/new-mcp", configuration: testConfiguration)
    #expect(result.contains("[mcp_servers.\"scratchpad\"] # quoted table with legal comment"))
    #expect(result.contains("[mcp_servers.\"scratchpad\".env] # env settings"))
    #expect(result.contains("command = \"/tmp/new-mcp\" # replace command only"))
    #expect(!result.contains("args ="))
    #expect(result.contains("enabled = true"))
    #expect(result.contains("startup_timeout_ms = 9000"))
    #expect(result.contains("SCRATCHPAD_MIRROR = \"true\" # preserve mirror policy"))
    #expect(result.contains("SCRATCHPAD_MIRROR_PATH = \"/tmp/synthetic-mirror\""))
    #expect(result.contains("KEEP = \"value\""))
    #expect(result.contains("theme = \"dark\""))
    #expect(result.components(separatedBy: "[mcp_servers.\"scratchpad\"]").count == 2)
}

@Test func codexPreviewFailsClosedOnUnsupportedScratchpadTables() {
    let existing = "[[mcp_servers.scratchpad]]\ncommand = \"old\"\n"
    #expect(throws: ClientConfigurationError.self) {
        try ClientConfiguration.codexPreview(existing: existing, executable: "/tmp/new-mcp", configuration: testConfiguration)
    }
}

@Test func codexPreviewHonorsArrayTableBoundaryAndFailsClosedOnAmbiguousForms() throws {
    let existing = """
    [mcp_servers.scratchpad]
    command = "old"
    enabled = true
    [[telemetry.routes]]
    endpoint = "https://example.test"
    """
    let result = try ClientConfiguration.codexPreview(existing: existing, executable: "/tmp/new-mcp", configuration: testConfiguration)
    #expect(result.contains("[[telemetry.routes]]"))
    #expect(result.contains("endpoint = \"https://example.test\""))
    #expect(!result.contains("[mcp_servers.scratchpad.env]"))
    #expect(result.range(of: "command = \"/tmp/new-mcp\"")!.lowerBound < result.range(of: "[[telemetry.routes]]")!.lowerBound)

    let unsupported = [
        "[mcp_servers.\"scrat\\u0063hpad\"]\ncommand = \"old\"\n",
        "mcp_servers.scratchpad.command = \"old\"\n",
        "[mcp_servers.scratchpad]\nenv = { SCRATCHPAD_URL = \"old\" }\n",
        "[mcp_servers.scratchpad]\nargs = [\n  \"--legacy\"\n]\n"
    ]
    for value in unsupported {
        #expect(throws: ClientConfigurationError.self) {
            try ClientConfiguration.codexPreview(existing: value, executable: "/tmp/new-mcp", configuration: testConfiguration)
        }
    }
}

@Test func stopUsesExtendedControlIPCDeadline() {
    #expect(ServiceClient.timeout(for: "stop") == 120)
    #expect(ServiceClient.timeout(for: "pause_delivery") == ServiceClient.defaultTimeout)
}

@Test func claudePreviewMergesEnvironmentAndRetainsUnrelatedSettings() throws {
    let existing = Data(#"{"theme":"dark","mcpServers":{"other":{"command":"other"},"scratchpad":{"command":"old","env":{"SCRATCHPAD_MIRROR":"true","KEEP":"value"}}}}"#.utf8)
    let result = try ClientConfiguration.jsonPreview(existing: existing, executable: "/tmp/synthetic-mcp", configuration: testConfiguration)
    let root = try #require(JSONSerialization.jsonObject(with: result) as? [String: Any])
    #expect(root["theme"] as? String == "dark")
    let servers = try #require(root["mcpServers"] as? [String: Any])
    #expect(servers["other"] != nil)
    let scratchpad = try #require(servers["scratchpad"] as? [String: Any])
    #expect(scratchpad["command"] as? String == "/tmp/synthetic-mcp")
    #expect(scratchpad["args"] == nil)
    let environment = try #require(scratchpad["env"] as? [String: String])
    #expect(environment["KEEP"] == "value")
    #expect(environment["SCRATCHPAD_MIRROR"] == "true")
    #expect(environment["SCRATCHPAD_URL"] == nil)
    #expect(environment["SCRATCHPAD_PUBLIC_KEY"] == nil)
    #expect(environment["SCRATCHPAD_SIGNING_KEY"] == nil)
}

@Test func malformedClientConfigurationIsRejectedWithoutReplacement() {
    #expect(throws: (any Error).self) {
        try ClientConfiguration.jsonPreview(existing: Data("[]".utf8), executable: "/tmp/mcp", configuration: testConfiguration)
    }
    #expect(throws: (any Error).self) {
        try ClientConfiguration.jsonPreview(existing: Data(#"{"mcpServers":[]}"#.utf8), executable: "/tmp/mcp", configuration: testConfiguration)
    }
}

@Test func reviewedApplyRejectsAChangedSelectionAndConcurrentEdits() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent("scratchpad-config-review-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }

    let selected = directory.appendingPathComponent("codex.toml")
    let other = directory.appendingPathComponent("other.toml")
    let original = Data("model = \"before\"\n".utf8)
    let proposed = Data("model = \"after\"\n".utf8)
    let different = Data("[mcp_servers.other]\ncommand = \"keep\"\n".utf8)
    try original.write(to: selected)
    try different.write(to: other)

    let changedPathPreview = ReviewedConfigFile(path: selected.path, originalContents: original, proposedContents: proposed)
    #expect(throws: (any Error).self) {
        try ClientConfiguration.applyReviewedFile(changedPathPreview, selectedURL: other)
    }
    #expect(try Data(contentsOf: selected) == original)
    #expect(try Data(contentsOf: other) == different)

    let concurrentPreview = ReviewedConfigFile(path: selected.path, originalContents: original, proposedContents: proposed)
    let concurrentEdit = Data("model = \"another editor's change\"\n".utf8)
    try concurrentEdit.write(to: selected)
    #expect(throws: (any Error).self) {
        try ClientConfiguration.applyReviewedFile(concurrentPreview, selectedURL: selected)
    }
    #expect(try Data(contentsOf: selected) == concurrentEdit)
}

@Test func reviewedApplyAllowsCreateAndRejectsSymbolicLinkTargets() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent("scratchpad-config-symlink-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: directory) }

    let missing = directory.appendingPathComponent("new.json")
    let contents = Data("{}\n".utf8)
    let createPreview = ReviewedConfigFile(path: missing.path, originalContents: nil, proposedContents: contents)
    try ClientConfiguration.applyReviewedFile(createPreview, selectedURL: missing)
    #expect(try Data(contentsOf: missing) == contents)

    let target = directory.appendingPathComponent("target.json")
    let link = directory.appendingPathComponent("link.json")
    try contents.write(to: target)
    try FileManager.default.createSymbolicLink(at: link, withDestinationURL: target)
    let linkPreview = ReviewedConfigFile(path: link.path, originalContents: contents, proposedContents: Data("{\"changed\":true}\n".utf8))
    #expect(throws: (any Error).self) {
        try ClientConfiguration.applyReviewedFile(linkPreview, selectedURL: link)
    }
    #expect(try Data(contentsOf: target) == contents)
}

@Test func commandOnlySetupRemovesLegacyConnectionSettings() throws {
    let existing = "[mcp_servers.scratchpad]\ncommand = \"old\"\n[mcp_servers.scratchpad.env]\nSCRATCHPAD_URL = \"https://old.example.test\"\nSCRATCHPAD_PUBLIC_KEY = \"/tmp/old.pub\"\nSCRATCHPAD_SIGNING_KEY = \"/tmp/old\"\n[ui]\ntheme = \"dark\"\n"
    let result = try ClientConfiguration.codexPreview(existing: existing, executable: "/tmp/mcp", configuration: testConfiguration)
    #expect(!result.contains("SCRATCHPAD_URL"))
    #expect(!result.contains("SCRATCHPAD_PUBLIC_KEY"))
    #expect(!result.contains("SCRATCHPAD_SIGNING_KEY"))
    #expect(!result.contains("[mcp_servers.scratchpad.env]"))
    #expect(result.contains("theme = \"dark\""))
    let generic = try ClientConfiguration.genericEntry(executable: "/tmp/mcp", configuration: testConfiguration)
    let entry = try #require(JSONSerialization.jsonObject(with: generic) as? [String: String])
    #expect(entry == ["command": "/tmp/mcp"])
}
